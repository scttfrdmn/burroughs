package interp

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// global is one global's storage: a slot and the declared type that says how to read it.
//
// **A numeric slot and a reference slot, not one of each per global** — the same split
// `stack` makes, for 0002's reason, and kept here because a global's value crosses between
// the two: `global.get` of an externref must push onto `stack.refs`, and a `uint64` holding
// a `ref` would be the tagged-value design 0002 declined. Which of the three fields is live
// is decided by `typ`, never by inspecting the slots, because a null reference and
// the integer zero are the same bits and only the type tells them apart.
//
// **`numHi` is grave #239's fix, and it is the storage half of it.** A `v128` occupies two adjacent
// numeric slots everywhere a slot is a thing (decision 0024), and this struct had one — so a
// `(global v128 …)` had nowhere to keep its upper 64 bits even after the initializer evaluated
// correctly. That is why the grave could not be closed in the evaluator alone: an instantiation-only
// fix would accept the module and then hand `global.get` a zero-filled high half, which is a wrong
// answer where the previous behaviour was an honest refusal. `get` and `set` below are the other
// half. Unused — and therefore harmless to omit — for every other type, which is exactly how it
// stayed missing.
//
// `mutable` is retained rather than checked: writing an immutable global is `global.set`'s
// validation verdict (#9), not a runtime event, so this engine records the fact and does
// not enforce it. Stated because the field otherwise reads as an unenforced guard — see
// globalFor on why the check is absent rather than forgotten.
type global struct {
	typ     binary.ValType
	mutable bool

	// mod is the module `typ`'s type index is read in, when `typ` is an indexed reference form
	// — the *defining* module, which is not always the module doing the exporting.
	//
	// **On the allocation rather than on the Extern, and the re-export case is why** (#368).
	// The reference does not need the field at all: `subst_of inst` resolves a runtime object's
	// type to `Def dt` at instantiation, so `type_of_global` hands back a self-contained type
	// and `externtype_of` can be read from any instance holding the object. This engine's types
	// stay indexed, so the resolution environment has to be stored somewhere, and the only
	// place that survives a re-export is the object: a module that imports a global and exports
	// it again shares this same *global, while `Instance.Export`'s own instance is the
	// re-exporter, whose type section the index does not belong to.
	mod *binary.Module

	num   uint64
	numHi uint64
	ref   ref
}

// newGlobal evaluates a global's initializer and allocates its storage.
//
// **Evaluated against the instance as it stands**, which is what makes the caller's ordering
// load-bearing: `eval.ml:1206`'s `init_global` folds over the globals in index order and
// evaluates each `eval_const inst c` against the *partially built* instance, so `(global i32
// (global.get 0))` reads a global initialized one step earlier. An engine that allocated all
// slots first and then evaluated would produce zero for that vector instead of the earlier
// global's value — a wrong answer, not a missing feature, and `global.wast:17` (`(global $z1
// i32 (global.get 0))`) is exactly that vector.
//
// The initializer runs through the full interpreter for `constExpr`'s reason: the
// reference's const production *is* the instruction grammar (`decode.ml:983`), so
// pattern-matching the constant forms would make `(global i32 (i32.add …))` silently wrong
// instead of honestly unimplemented.
//
// **One call, not a branch on `IsRef()`.** The branch was where grave #239 lived: the numeric arm
// asked for one slot unconditionally, so `v128` — a type neither arm was written for — took the
// numeric one and failed its arity check. `constExpr` derives the shape from `g.Type` via
// `countByArray`, so the three fields are assigned from the one result and a fourth shape arriving in
// `binary.ValType` is a change to `countByArray`, not to this function.
func (in *Instance) newGlobal(g binary.Global) (*global, error) {
	v, err := in.constExpr(g.Init, g.Type, "a global initializer")
	if err != nil {
		return nil, err
	}
	return &global{
		typ: g.Type, mutable: g.Mutable, mod: in.mod,
		num: v.lo, numHi: v.hi, ref: v.ref,
	}, nil
}

// globalFor resolves a global index to its storage. The *only* place that does, which is what
// keeps its two failure modes from being half-remembered elsewhere — memoryFor's rule (grave
// #78/#105/#106: two places knowing how to turn an index into a thing is how they drift), and
// the reason this is a method rather than an inline bounds check at each of the two arms.
//
// `what` names the holder of the index for memoryFor's reason: "instruction" versus "global
// initializer" sends the reader to a different line of their module.
//
// **No mutability check here, and its absence is a layering decision.** Writing an immutable
// global is `global.set`'s *validation* verdict — the spec's `global is immutable` is an
// `assert_invalid` string, and `global.wast:249` onward assert exactly that — so enforcing it
// here would put #9's answer somewhere #9 cannot be tested from, and would make this package
// judge a module. The `mutable` field is recorded and unread until the validator wants it.
func (in *Instance) globalFor(what string, idx uint64) (*global, error) {
	if idx >= uint64(len(in.globals)) {
		return nil, fmt.Errorf("%w: %s names global %d of %d",
			ErrNotValidated, what, idx, len(in.globals))
	}
	if in.globals[idx] == nil {
		// A reserved slot with nothing in it, reported by *which* nothing — memoryFor's
		// split, and it transfers unchanged because the index space's shape is the same
		// fact for every extern kind. Below the import offset is an imported global nothing
		// supplied; above it, a declared global whose initializer failed for a reason the trap
		// channel could not carry. The logic is unchanged by linking arriving and the message is
		// not, both for memoryFor's reasons.
		if idx < uint64(in.mod.ImportedGlobals()) {
			return nil, fmt.Errorf("%w: global %d is an import nothing supplied (contract §3)",
				ErrUnsupported, idx)
		}
		return nil, fmt.Errorf("%w: global %d was declared but not initialized: %w",
			ErrNotValidated, idx, in.deferred)
	}
	return in.globals[idx], nil
}

// globalShape is which of a global's three storage layouts its declared type selects.
//
// **One authority for the dispatch, because there are now two consumers of it.** `get` pushes
// onto the stack and `value` crosses the public boundary, and both answer the identical
// question — reference, v128, or one numeric slot — off the identical fact. Written as an enum
// the moment the second consumer arrived (#323's `(get …)` read path) rather than as a second
// `switch`: two places that know how to turn a declared type into a layout is graves
// #78/#105/#106's shape, the one `globalFor` and `memoryFor` are already single-sited against.
// The `v128` arm in particular is grave #239, whose whole lesson was that the read-back half
// can be missing while the write half is right — so a *third* consumer arriving must not be
// able to get the arm count wrong.
type globalShape int

const (
	shapeNum  globalShape = iota // one numeric slot — every type but v128 and the refs
	shapeV128                    // two numeric slots, hi and lo (decision 0024)
	shapeRef                     // the reference slot
)

// shape reports which layout this global's storage uses.
//
// Dispatched on the *declared type*, not on the slots' contents, for the reason `global`'s own
// comment gives: a null ref and an integer zero are indistinguishable bits, and only the type
// tells them apart.
func (g *global) shape() globalShape {
	switch {
	case g.typ.IsRef():
		return shapeRef
	case g.typ == binary.V128:
		return shapeV128
	default:
		return shapeNum
	}
}

// get pushes the global's value onto the matching half of the stack.
//
// The `v128` arm is grave #239's read-back half. Its absence is what makes an
// instantiation-only fix insufficient: with the evaluator widened and this arm still missing, a
// `(global v128 (v128.const i32x4 1 2 3 4))` module would instantiate, and `global.get` would push a
// single slot — leaving the *next* pop to read whatever sat beneath it. So the vector that closes the
// grave has to read all four lanes back, not merely instantiate.
func (g *global) get(st *stack) {
	switch g.shape() {
	case shapeRef:
		st.pushRef(g.ref)
	case shapeV128:
		st.pushV128(g.numHi, g.num)
	default:
		st.pushNum(g.num)
	}
}

// value reads the global out as a boundary Value, for the script `(get …)` action (#323).
//
// **The read-only twin of `get`, sharing its dispatch and not its destination.** Where `get`
// pushes onto the interpreter's two-array stack, this hands back the one-struct form the public
// boundary carries, built exactly the way `invokeIndex` builds a result of the same type — same
// `fromRef`, same hi/lo assignment for a v128, same bare `Bits` otherwise. Written as a sibling
// rather than as a call through `get` and a pop, because routing a reference through a stack to
// read it back would convert `ref → Value → ref` for no reason and put a second `toRef`/`fromRef`
// round trip in a path whose whole job is one read.
//
// No mutability question here, deliberately: reading an immutable global is legal, and the
// unenforced `mutable` field's story is at `globalFor`.
func (g *global) value() Value {
	switch g.shape() {
	case shapeRef:
		return fromRef(g.ref, g.typ)
	case shapeV128:
		return Value{Type: g.typ, Bits: g.num, Hi: g.numHi}
	default:
		return Value{Type: g.typ, Bits: g.num}
	}
}

// set pops a value into the global.
//
// Returns the layering debt rather than trapping on an empty stack, which is `needNum`'s
// contract: underflow is `type mismatch`, a verdict this package does not issue.
func (g *global) set(st *stack) error {
	switch {
	case g.typ.IsRef():
		if err := st.needRef(1); err != nil {
			return err
		}
		g.ref = st.popRef()
	case g.typ == binary.V128:
		// **Two slots asked for as two, not as one twice.** `needNum(2)` is the whole underflow
		// question for a v128, and `popV128` returns (hi, lo) in that order — `pushV128`'s own
		// order — so `lo` is the stack's true top. Transposing the two here is a wrong answer no
		// arity check can see, which is why the destinations are named rather than positional.
		if err := st.needNum(2); err != nil {
			return err
		}
		g.numHi, g.num = st.popV128()
	default:
		if err := st.needNum(1); err != nil {
			return err
		}
		g.num = st.popNum()
	}
	return nil
}
