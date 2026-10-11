// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TableFunc is a function read out of a table slot and held for a later call — the component model's
// `thread.new-indirect`, which resolves its entry point when the Thread is CREATED and runs it when the
// Thread is first RESUMED (`canon_thread_new_indirect` reads `ftbl.get(fi)` up front, and its
// `thread_func` calls the result later).
//
// It is the funcref's own `(instance, function index)` pair and nothing else, so a call goes through
// that instance's `invokeIndex` exactly as an exported call does.
type TableFunc struct {
	inst *Instance
	addr uint32
}

// ResolveTableFunc reads slot `slot` of table `tbl` and resolves it to a callable function of type
// `(i32) -> ()`, which is `thread.new-indirect`'s entry type for a 32-bit address space.
//
// # It is `resolveCallIndirect`'s read, step for step, so three graves stay covered by the code that
// already covers them
//
// `tab.load` for the bounds check (`undefined element i`), `uninitializedElem` for a null slot, and
// `funcRefTarget` for the resolution — which is where grave #163 (resolve against the funcref's own
// instance, never the caller's), grave #246 (a zero `ref` that is not null) and #669 (a non-null ref
// with no defining instance) are all handled. A second resolution here would be a second place for
// each of those to come back.
//
// # The type check is against the CALLEE, and that leaves one recorded divergence
//
// The reference's check is `trap_if(f.t != ft)`, where `ft` is the canon's declared core type —
// validated to be `(i32)->()` or `(i64)->()`. The component loader does not decode `core:type`
// sections at all, so `ft` is an index this engine has never read. What is checked instead:
//
//   - the callee is `(i32) -> ()`, by the structural comparison `call_indirect` uses for a host callee
//     and the declared type for a defined one;
//   - an `(i64) -> ()` callee — the memory64 form — is refused by name as unsupported, because without
//     `ft` the engine cannot tell a matching i64 pair from a mismatched one.
//
// **The divergence left:** a component whose declared `ft` is `(i64)->()` but whose table entry is
// `(i32)->()` traps in the reference and runs here. That is a guest whose own canon and table disagree
// on width; closing it needs `core:type` decoding, which is its own piece of work.
func ResolveTableFunc(tbl Extern, slot uint32) (TableFunc, error) {
	if tbl.tab == nil {
		return TableFunc{}, fmt.Errorf("%w: thread.new-indirect's table operand is not a table",
			ErrUnsupportedOp)
	}
	i := uint64(slot)
	r, err := tbl.tab.load(i) // `undefined element i` when out of bounds
	if err != nil {
		return TableFunc{}, err
	}
	if r.Null {
		return TableFunc{}, uninitializedElem(i)
	}
	t, err := funcRefTarget(r, fmt.Sprintf("table slot %d", slot))
	if err != nil {
		return TableFunc{}, err
	}
	var got *binary.FuncType
	if t.host != nil {
		got = &t.host.ft
	} else {
		got, err = t.inst.funcType(t.fn)
		if err != nil {
			return TableFunc{}, err
		}
	}
	i32Entry := &binary.FuncType{Params: []binary.ValType{binary.I32}}
	i64Entry := &binary.FuncType{Params: []binary.ValType{binary.I64}}
	switch {
	case funcTypesMatch(got, i32Entry):
		return TableFunc{inst: r.Inst, addr: r.Addr}, nil
	case funcTypesMatch(got, i64Entry):
		return TableFunc{}, fmt.Errorf("%w: thread.new-indirect's entry in table slot %d is (i64) -> (), the "+
			"memory64 form; this engine does not decode core:type sections, so it cannot check that form "+
			"against the canon's declared type and refuses it rather than guessing", ErrUnsupportedOp, slot)
	default:
		return TableFunc{}, &Trap{Reason: fmt.Sprintf("thread.new-indirect: table slot %d holds %s, "+
			"want (i32) -> ()", slot, funcTypeString(got))}
	}
}

// InvokeCMThread calls the resolved function with `args`, as component-model Thread `cmThread`.
//
// **Through the funcref's own instance's `invokeIndex`**, so it inherits every invariant an exported
// call has rather than restating them: the closed-instance refusal (ADR 0071 row 4), `enterGuest` and
// the §4 B-MM-1 edge, the caller count SP-2 reads (ADR 0067), the unwinding repair and terminal-unwind
// `recover` (ADR 0070/0071), and re-export delegation for an imported function. The fault report is
// taken first, as `Invoke` does, so a thread that trapped earlier is reported once and not again.
func (f TableFunc) InvokeCMThread(cmThread uint32, args ...Value) ([]Value, error) {
	if fault := f.inst.world.takeFaultReport(); fault != nil {
		return nil, fault
	}
	return f.inst.invokeIndex(f.addr, "thread.new-indirect entry", args, cmThread)
}
