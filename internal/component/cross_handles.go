// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// A cross-component call's handles (#1281).
//
// # A handle is an index into its holder's table, so it cannot cross as a word
//
// `own`, `borrow`, `stream`, `future` and `error-context` flatten to one i32, and the sync arm of
// `bindCrossComponent` passed it through with the scalars. But each nested instance has its own tables
// (`walkComponent` builds `hostRes` and `async` per instance), so the caller's index named whatever sat at
// that index in the callee's. Measured on `xc-own-param.wasm`: the parent passes its stdout handle, index 1,
// and the child's index 1 is its own stderr handle. wasmtime 49.0.3 prints the child's line on stdout and
// hands the child index 2; Burroughs printed it on stderr, handed the child 1, and the child's drop of the
// handle it was given destroyed its own stderr handle.
//
// The pin never passes the index. It **moves the handle**: the caller's lift takes it out of the caller's
// table (def:lift_own:"h = cx.inst.handles.remove(i)") and the callee's lower adds it to the callee's
// (def:lower_own:"return cx.inst.handles.add(h)"), and a result moves back the same way.
//
// # What moves, and what is refused by name
//
//   - **A top-level `own<R>` of a host resource, as a parameter or as the result of a sync call, moves.** The
//     object leaves the caller's table and is minted into the callee's (`hostResources.give`), which is the
//     whole of def:lift_own and def:lower_own for a resource whose implementation is the host.
//   - **A `borrow` is refused.** The pin lends it for the subtask (def:lift_borrow:"cx.borrow_scope.add_lender(h)"),
//     releases the lend at def:Subtask.deliver_resolve, and counts it against the callee's task, which traps
//     if a borrow outlives it (def:Task.return_:"trap_if(self.num_borrows > 0)"). This engine models no borrow
//     scope, so moving the handle without that accounting would admit what the pin traps.
//   - **A `stream` or `future` is refused.** Moving an end is def:lift_async_value and def:lower_async_value,
//     but a stream's two ends share copy state that is guarded by the mutex of the one table both live in
//     (`readableStreamEnd`, `writableStreamEnd`), and a copy wakes the reader through that table. With the
//     ends in two instances' tables, each side would touch the other's state under a different lock.
//   - **An `error-context`, an `own` of a resource that is not the host's (a guest-defined one, #1251), and
//     any handle inside a record, tuple, option, result, variant or list are refused**, as is a handle in a
//     signature that crosses through the adapter or through an async lower: none of those paths moves one.
//
// A signature with no handle in it is untouched, so every cross-component call that worked still does.

// crossOwn is one top-level `own<R>` parameter: the flat word it occupies and its host resource type.
type crossOwn struct {
	word int
	t    *hostResource
}

// crossHandles is what a cross-component signature's handles need at the boundary: the `own<R>` parameters
// to move from the caller's table into the callee's, and the `own<R>` result to move back. The zero value
// moves nothing.
type crossHandles struct {
	params []crossOwn
	result *hostResource
}

// moves reports whether the signature carries a handle that moves.
func (p crossHandles) moves() bool { return len(p.params) > 0 || p.result != nil }

// crossHandlesOf plans a cross-component call's handles, or refuses its signature by name when it carries a
// handle this boundary does not move (see the file comment). The signature is the callee's lift's, so an own's
// resource is resolved by the callee's walk (`compFunc.resAt`). Every instance in a composition shares the
// host's resource types, so the type it resolves to is the caller's too.
func crossHandlesOf(callee *compFunc) (crossHandles, error) {
	var plan crossHandles
	sig := callee.sig
	if sig == nil {
		return plan, nil
	}
	word, counted := 0, true
	for _, p := range sig.Params {
		where := fmt.Sprintf("parameter %q", p.Name)
		if p.Type.Kind == VOwn {
			t, err := callee.crossOwnType(where, p.Type)
			if err != nil {
				return crossHandles{}, err
			}
			if !counted {
				return crossHandles{}, crossHandleRefusal(where, VOwn, "the parameters before it cannot be "+
					"counted in flat words, so the word to move cannot be found")
			}
			plan.params = append(plan.params, crossOwn{word: word, t: t})
		} else if k, ok := handleIn(p.Type, 0); ok {
			return crossHandles{}, crossHandleRefusal(where, k, whyNotMoved(k, p.Type.Kind))
		}
		if n, ok := flatWords(p.Type); ok {
			word += n
		} else {
			counted = false
		}
	}
	if sig.Result != nil {
		if sig.Result.Kind == VOwn {
			t, err := callee.crossOwnType("result", *sig.Result)
			if err != nil {
				return crossHandles{}, err
			}
			plan.result = t
		} else if k, ok := handleIn(*sig.Result, 0); ok {
			return crossHandles{}, crossHandleRefusal("result", k, whyNotMoved(k, sig.Result.Kind))
		}
	}
	return plan, nil
}

// crossOwnType resolves a top-level own's resource to the host type whose table entries it moves, through the
// callee's type index space. `vt.Resource` is not asked: in a nested component it is the identity spelled by
// the nested component's own import name (`streams::output-stream`), which names no host type.
func (f *compFunc) crossOwnType(where string, vt ValType) (*hostResource, error) {
	if f.resAt != nil {
		if t := f.resAt(vt.Ref); t != nil {
			return t, nil
		}
	}
	return nil, crossHandleRefusal(where, VOwn, fmt.Sprintf("its resource %q is not one the host implements, "+
		"and only a host resource's own is moved between tables (a guest-defined resource is #1251's)",
		vt.Resource))
}

// handleIn reports the first handle kind anywhere in vt, itself included.
func handleIn(vt ValType, depth int) (ValKind, bool) {
	if depth > 32 {
		return 0, false // unmodeledValKind's bound, for a hand-built cyclic type
	}
	sub := func(p *ValType) (ValKind, bool) {
		if p == nil {
			return 0, false
		}
		return handleIn(*p, depth+1)
	}
	switch vt.Kind {
	case VOwn, VBorrow, VStream, VFuture, VErrorContext:
		return vt.Kind, true
	case VList, VOption:
		return sub(vt.Elem)
	case VResult:
		if k, ok := sub(vt.Ok); ok {
			return k, true
		}
		return sub(vt.Err)
	case VVariant:
		for _, c := range vt.Cases {
			if k, ok := sub(c.Type); ok {
				return k, true
			}
		}
	case VRecord:
		for _, f := range vt.Fields {
			if k, ok := handleIn(f.Type, depth+1); ok {
				return k, true
			}
		}
	case VTuple:
		for _, e := range vt.Elems {
			if k, ok := handleIn(e, depth+1); ok {
				return k, true
			}
		}
	default:
	}
	return 0, false
}

// whyNotMoved is the refusal's reason for a handle kind found where the boundary does not move it. `outer` is
// the kind of the value it was found in, which differs from k when the handle is nested.
func whyNotMoved(k, outer ValKind) string {
	switch k {
	case VBorrow:
		return "the pin lends a borrow for the subtask and counts it against the callee's task " +
			"(def:lift_borrow, def:lower_borrow), and this engine models no borrow scope"
	case VStream, VFuture:
		return "moving an end between two instances' tables (def:lift_async_value, def:lower_async_value) " +
			"would leave its two ends' copy state under two tables' locks"
	case VErrorContext:
		return "an error-context is an index into the caller's table, and no table here moves one"
	default:
		return fmt.Sprintf("an own inside a %s is not at a position this boundary moves", valKindName(outer))
	}
}

// crossHandleRefusal is the by-name refusal of a cross-component signature carrying a handle this boundary
// does not move. It is bound at bind and fires at call, the shape every refusal in bindCrossComponent has,
// because the resolver has no error channel.
func crossHandleRefusal(where string, k ValKind, why string) error {
	return fmt.Errorf("%w: a cross-component call whose %s carries %s is not carried yet: %s, so the "+
		"caller's index would name whatever the callee's table holds there (#1281)",
		ErrUnsupportedForm, where, valKindName(k), why)
}

// moveParams moves each own parameter from the caller's table into the callee's, returning the callee's
// flat words (def:lift_own, then def:lower_own). The words are copied, so the caller's slice is untouched.
func (p crossHandles) moveParams(from, to *hostResources, args []interp.Value) ([]interp.Value, error) {
	if len(p.params) == 0 {
		return args, nil
	}
	out := append([]interp.Value(nil), args...)
	for _, o := range p.params {
		if o.word >= len(out) {
			return nil, &interp.Trap{Reason: fmt.Sprintf("a cross-component call's own parameter is flat "+
				"word %d of %d", o.word, len(out))}
		}
		idx, err := from.give(o.t, out[o.word].Int32(), to)
		if err != nil {
			return nil, err
		}
		out[o.word] = interp.I32(idx)
	}
	return out, nil
}

// moveResult moves an own result from the callee's table into the caller's, returning the caller's flat
// words. A signature whose result is not an own returns them unchanged.
func (p crossHandles) moveResult(from, to *hostResources, flat []interp.Value) ([]interp.Value, error) {
	if p.result == nil {
		return flat, nil
	}
	if len(flat) != 1 {
		return nil, &interp.Trap{Reason: fmt.Sprintf("a cross-component call's own result arrived as %d flat "+
			"value(s), want 1", len(flat))}
	}
	idx, err := from.give(p.result, flat[0].Int32(), to)
	if err != nil {
		return nil, err
	}
	return []interp.Value{interp.I32(idx)}, nil
}

// give moves an own<t> from this table into another (def:lift_own here, def:lower_own there): the handle
// leaves this table, refused as the pin refuses it if it is not held, names another type, is lent or is a
// borrow, and its object is minted into `to` under a rep of `to`'s. The two locks are never held together.
func (r *hostResources) give(t *hostResource, idx int32, to *hostResources) (int32, error) {
	r.mu.Lock()
	rep, err := r.table.LiftOwn(int(idx), t.rt)
	if err != nil {
		r.mu.Unlock()
		return 0, resourceTrap("own transfer", t, idx, err)
	}
	obj := r.objs[rep]
	delete(r.objs, rep)
	r.mu.Unlock()
	return to.mint(t, obj), nil
}
