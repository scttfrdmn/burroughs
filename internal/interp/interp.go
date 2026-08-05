package interp

import (
	"errors"
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// Instance is a module ready to be invoked.
//
// **Not an instantiation, and the name is the honest one available.** Real instantiation
// (contract §3) allocates memories and tables, evaluates global initializers, runs the start
// function, and links imports; none of that exists yet, and none of it is needed to invoke an
// exported function whose body is arithmetic. So this holds the decoded module and nothing else,
// and the moment a memory or a global is executable this type grows the state — which is why it
// is a struct with one field rather than an alias for `*binary.Module`.
//
// It is deliberately constructed by a function that **cannot fail**. An `Instantiate` returning
// an error would be a second place judging modules, and the judgement it would be making is
// #9's: every failure a real instantiation can report (a global initializer that is not a
// constant expression, an unlinkable import, a table's element type disagreeing) is a
// *validation* verdict, and inventing an interpreter-shaped version of it here would put the
// validator's answers under a name that hides them. What this package refuses, it refuses at
// the point of execution, with an error that says the engine has no arm for the instruction —
// never with a claim about the module.
type Instance struct {
	mod *binary.Module
}

// New wraps a decoded module for invocation.
//
// The module is retained, not copied: `binary.Module`'s payloads already alias the caller's
// image (the decoder's in-place posture), so a copy here would be a second aliasing of the same
// bytes under the illusion of ownership.
func New(m *binary.Module) *Instance { return &Instance{mod: m} }

// Module returns the instance's module.
func (in *Instance) Module() *binary.Module { return in.mod }

// Trap is a wasm trap: the module executed correctly and the *program* went wrong.
//
// **A distinct type from every other error this package returns, because the suite scores the
// two in opposite directions.** `assert_trap` wants a trap and `assert_return` wants a value, so
// an engine that reported "integer divide by zero" the same way it reports "this opcode has no
// arm yet" would make 4963 assert_trap vectors indistinguishable from 4963 engine gaps — the
// fail-column dilution decision 0010 exists to prevent, one layer down.
//
// The Reason strings are the spec's own trap texts, because that is what `assert_trap`'s second
// argument matches against. They are *testimony* in the sense the doctrine means: a trap
// reporting the wrong reason is right about the verdict and wrong about the evidence, and the
// suite reads far enough to catch it.
type Trap struct {
	Reason string
}

func (t *Trap) Error() string { return "trap: " + t.Reason }

// The trap reasons the numeric core can produce, spelled as the spec spells them.
//
// Three of them, and that is the complete set for arithmetic — every other trap in the suite
// (`out of bounds memory access`, `undefined element`, `uninitialized element`, `indirect call
// type mismatch`) belongs to a construct this package does not execute yet, and `unreachable`
// is declared beside the instruction that raises it (exec.go). Named constants rather than
// inline strings so that a fourth one arriving is a declaration rather than a literal in a
// switch arm.
//
// It said "Four of them" over a block of three, having counted `unreachable` and then not
// declared it here — a comment asserting a property of its own declaration block, which is the
// cheapest kind to check and was not checked. Corrected by counting the vars.
var (
	trapDivByZero   = &Trap{Reason: "integer divide by zero"}
	trapIntOverflow = &Trap{Reason: "integer overflow"}
	trapBadConvert  = &Trap{Reason: "invalid conversion to integer"}
)

// ErrUnsupportedOp is the engine saying it has no arm for an instruction.
//
// **Not a verdict on the module, and the distinction is the whole reason it is a separate
// sentinel from Trap.** The module is well-formed and the instruction is a real one; what is
// missing is engine, and the honest report names the engine's gap. That makes the board's
// failure bucket a work plan keyed by opcode — `interp: no arm for opcode 0xfd 0x03` names SIMD,
// `0x3f` names memory — which is the bucketed-failures discipline pointed at this layer.
//
// It is reported when the instruction is *reached*, never by scanning a body in advance. A
// pre-scan would refuse a function over an instruction on a path that never executes, which
// would be an engine gap masquerading as a module property; and it would make the pass column
// claim less than the engine actually did. With no control flow in the numeric core the two are
// the same set today, which is exactly when the cheaper-looking choice should be inspected
// rather than taken.
var ErrUnsupportedOp = errors.New("interp: no arm for opcode")

// ErrNotValidated is the interpreter declining to execute something #9 would have rejected.
//
// **A declared layering debt, not a validation verdict** (#6's declared-and-tracked ruling). The
// validator does not exist, so a body reaching this package can index a local that is not there
// or pop a stack that is empty — and the choice is between panicking, checking, and being wrong.
// This is the check: it returns rather than panics, it says which invariant failed, and it says
// in this comment that every one of its call sites becomes unreachable when #9 lands. A fuzz
// target over decoded-but-invalid modules is the reason it exists at all, because such a module
// is exactly what the decoder accepts and the validator would not.
//
// What it must never become is a spec verdict: `type mismatch` is the validator's string, and
// reporting it from here would put #9's answer in a place #9 cannot be tested from.
var ErrNotValidated = errors.New("interp: module reached the interpreter unvalidated")

// Invoke calls an exported function by name.
//
// Argument checking is by type and arity against the declared functype, and it happens *before*
// the frame is built — the boundary is where the static knowledge stops (see Value), so it is
// also where the check belongs. A host passing an i64 where an i32 is declared gets an error
// naming both types rather than a silently truncated slot.
//
// Results come back in stack order, which is declaration order: wasm pushes results left to
// right, so popping fills the slice from the end. Getting that backwards is invisible for the
// 12799 single-result vectors in the corpus and wrong for the 1188 multi-result ones, which is
// the shape of defect a majority-of-the-corpus check scores green.
func (in *Instance) Invoke(name string, args ...Value) ([]Value, error) {
	idx, ok := in.exportedFunc(name)
	if !ok {
		return nil, fmt.Errorf("interp: no exported function %q", name)
	}
	fn, ok := in.mod.DefinedFunc(idx)
	if !ok {
		// An exported *import*. The index is in the imported range, so there is no body
		// here to run — linking is contract §3 and not this phase. Reported as an
		// engine gap rather than a module fault, because the module is fine.
		return nil, fmt.Errorf("%w: exported function %q is an import (index %d)",
			ErrUnsupportedOp, name, idx)
	}
	ft, err := in.funcType(fn)
	if err != nil {
		return nil, err
	}
	if len(args) != len(ft.Params) {
		return nil, fmt.Errorf("interp: %q takes %d arguments, got %d", name, len(ft.Params), len(args))
	}
	// The frame's locals: parameters first, then the declared locals zeroed.
	//
	// One array, not two, and unlike the value stack that is *not* a design decision yet — a
	// ref-typed local is refused below, so no reference ever needs a slot here. When one does,
	// the parallel array joins the frame for the value stack's reason (0002's pinned
	// consequence), keyed by the same index: the validator knows each local's type, so each
	// index uses exactly one of the two arrays.
	nLocals := len(ft.Params) + len(fn.Locals)
	locals := make([]uint64, nLocals)
	for i, p := range ft.Params {
		if p.IsRef() {
			return nil, fmt.Errorf("%w: parameter %d of %q is %s", ErrUnsupportedOp, i, name, p)
		}
		if args[i].Type != p {
			return nil, fmt.Errorf("interp: %q parameter %d is %s, got %s", name, i, p, args[i].Type)
		}
		locals[i] = args[i].Bits
	}
	for i, l := range fn.Locals {
		if l.IsRef() {
			return nil, fmt.Errorf("%w: local %d of %q is %s", ErrUnsupportedOp, i, name, l)
		}
	}

	st := &stack{
		// Sized from the body rather than grown from empty: an instruction pushes at most
		// one numeric slot, so the body's length is a sufficient bound and there is exactly
		// one allocation per call. Not a correctness property — append would be correct —
		// but 0002 chose this form on a measurement, and paying an amortized regrow per
		// call in the hot loop would spend what the measurement bought.
		num: make([]uint64, 0, len(fn.Body)),
	}
	if err := in.run(fn, locals, st); err != nil {
		return nil, err
	}
	if len(st.num) != len(ft.Results) {
		// #9's arity check, arriving late. Stated as the layering debt it is rather than
		// dressed as `type mismatch`.
		return nil, fmt.Errorf("%w: %q declares %d results and left %d values on the stack",
			ErrNotValidated, name, len(ft.Results), len(st.num))
	}
	out := make([]Value, len(ft.Results))
	for i := len(out) - 1; i >= 0; i-- {
		t := ft.Results[i]
		if t.IsRef() {
			return nil, fmt.Errorf("%w: result %d of %q is %s", ErrUnsupportedOp, i, name, t)
		}
		out[i] = Value{Type: t, Bits: st.popNum()}
	}
	return out, nil
}

// exportedFunc resolves an export name to a function index.
//
// Linear over the export section, and deliberately not indexed: a map built per instance would
// be paid by every module and read by the handful the harness invokes. If a Go guest's export
// table ever becomes the hot path, the index belongs in Instance and is built once — which is
// the shape this comment exists to record rather than the thing to build now.
func (in *Instance) exportedFunc(name string) (uint32, bool) {
	for _, e := range in.mod.Exports {
		if e.Kind == binary.ExternFunc && e.Name == name {
			return e.Index, true
		}
	}
	return 0, false
}

// funcType resolves a function's declared type.
//
// The two failures here are #9's — a type index past the end of the type section, and a type
// index naming a struct or an array rather than a functype — and both are reported as the
// layering debt rather than as spec verdicts. The second is reachable today in the all-gates-on
// lane specifically, because `Module.Types` keeps struct and array slots so that GC type indices
// do not shift; a function declaring one of those slots is a module the validator rejects and
// the decoder accepts.
func (in *Instance) funcType(fn *binary.Func) (*binary.FuncType, error) {
	if int(fn.TypeIndex) >= len(in.mod.Types) {
		return nil, fmt.Errorf("%w: function declares type %d of %d",
			ErrNotValidated, fn.TypeIndex, len(in.mod.Types))
	}
	ct := &in.mod.Types[fn.TypeIndex]
	if ct.Kind != binary.CompFunc {
		return nil, fmt.Errorf("%w: function declares type %d, which is a %s",
			ErrNotValidated, fn.TypeIndex, ct.Kind)
	}
	return &ct.Func, nil
}
