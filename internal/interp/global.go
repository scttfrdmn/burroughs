package interp

import (
	"fmt"
	"sync"
	"sync/atomic"

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

	// num holds a numeric global's entire value and a v128 global's **low** 64 bits; numHi holds a
	// v128's high half and is unread for every other shape.
	//
	// **Atomic because a `global.set` races a `global.get` on a shared instance** (#573, decision
	// 0063). Nothing gates that: `Invoke` is an exported method on `*Instance` that two goroutines may
	// call at once — `TestAtomicRmwIsNotObservablyTornAcrossThreads` already does — so the two threads
	// reaching one `*global` need no threads proposal and no `Spawn` to exist.
	//
	// **The type carries the discipline, rather than a comment carrying it.** `atomic.LoadUint64(&num)`
	// over a plain `uint64` field would leave every plain read compiling silently, so the rule would
	// live here in prose and be enforced by review. As `atomic.Uint64` a plain read does not build.
	// Decision 0058 makes the same argument one struct over, for the same reason.
	//
	// **Word atomicity is not pair atomicity, and a v128 needs both** — see `mu` below. These two
	// fields are atomic for the numeric arm's sake and are *also* atomic on the v128 path, which is
	// redundant there and left in place: `num` serves both shapes, so its type cannot vary by shape.
	num   atomic.Uint64
	numHi atomic.Uint64

	// ref holds a reference global's value, **published as a pointer to an immutable 40-byte value**
	// rather than stored inline (#573's third arm, decision 0066).
	//
	// **Why not `mu`, which was one line away.** A `ref` is 40 bytes — 2.5× a v128 — which falsified the
	// premise the earlier ruling rested on (*"the only case above word width, and rare in practice"*),
	// and a hot `externref` `global.get` is not rare. Scott's ruling: *"`get` is one atomic load; `set`
	// allocates and swaps. Reads are the hot direction on a global and writes are rare, so this keeps
	// the common path free where a mutex would tax every get."* So the two shapes above word width sit
	// under two different mechanisms, v128 under `mu` and this one under an atomic pointer, for a reason
	// that is historical rather than principled — noted rather than resolved, on his order, with #625
	// carrying the question of whether v128 should follow.
	//
	// **The board falsified "free", and the basis is relative rather than absolute.** That last clause of
	// the ruling is quoted above as what was ruled, not as what is true: R1 forecast a read within the
	// null arm's excursion and `GetRef` came back **+17.19% (p=0.000)** against a null excursion of 0.05%,
	// which is +4.14 ns per get on native x86-64. It is not the allocation (the read path's `allocs/op` is
	// unchanged), not call overhead (both accessors inline), and not the atomic load, which would make
	// arm64 the worse column and instead makes it 6× better. So the argument for this mechanism is that a
	// read costs 4.14 ns where the same read under `mu` costs the pair #600 measured at 11.32 ns — 2.7×
	// cheaper, not free — while a `set` costs +32.29 ns and one 48-byte allocation. **Which way that trades
	// depends on a read:write ratio nothing here measures**: below roughly 3 reads per write the mutex
	// wins, and #640 is where that premise gets falsified. Scott's ruling on the board — *"a decision left
	// resting on a falsified premise is the shape this project keeps digging back out"* — is why the
	// falsification is recorded at the field and not only in decision 0066.
	//
	// **The immutability is what makes one load sufficient, and it is held by the accessors rather than
	// by prose.** `storeRef` copies its argument into a fresh cell, so no caller retains a writable
	// alias to a published value and a reader's `Load` needs nothing between it and its dereference.
	// That is the same shape decision 0065 gives the table and segment images one struct over: what a
	// reader holds is a whole value that nothing will overwrite, so the load is the whole
	// synchronisation.
	//
	// **A non-nil pointer is an invariant of construction**, established at `newGlobal` for every shape
	// — the store there is unconditional, so a numeric global's unread reference slot also holds a
	// cell. `loadRef` dereferences without a nil check on that invariant, and the invariant is worth
	// having in exactly one place: `newGlobal` is the only production constructor of a `*global`.
	ref atomic.Pointer[ref]

	// mu serialises the v128 arm's two-word access — held across both stores in `set` and both loads in
	// `get` and `value`.
	//
	// **A lock rather than a seqlock, and the choice was granted rather than measured against one.** The
	// ruling on #573 said *"a lock or seqlock, implementer's choice, measured in the slice"*: the cost of
	// the mechanism is what the slice owes, not a bake-off, and a bake-off would need two mechanisms in
	// one revision — which is exactly the comparison #618 records `ab.sh` cannot make. Decision 0061
	// already reaches for `sync.Mutex` for a two-places-one-fact problem of the same shape.
	//
	// **The measurement did show a v128 `global.get` dominated by the acquire, so the seqlock is filed
	// (#625) and this mutex stays.** On native x86-64 the read path costs +41.73% (p=0.000) against a
	// null arm excursion of 0.16%, and the whole of it is the `Lock`/`Unlock` pair: an atomic *load* is
	// a plain `MOVQ` there, so it adds nothing, and the pair prices out at 10.04 ns per access. That is
	// the pre-registered rollback condition, and the rollback was *file the successor*, not swap it —
	// two mechanisms in one revision is the comparison #618 records `ab.sh` cannot make. A seqlock's
	// readers do not write and so do not serialise against each other, but the win is TSO-side: arm64
	// shows no such cost, and a seqlock reader there needs fencing this mutex provides for free.
	//
	// Unconditional rather than allocated for v128 globals only: a `*sync.Mutex` is the same eight bytes
	// plus an allocation and a nil check, so the pointer buys nothing. Taken on the v128 arm alone, and
	// the numeric arm's freedom from it is the whole point of decision 0063's split.
	mu sync.Mutex
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
// threadGlobals builds one spawned thread's global storage — contract §2 T-6's mechanism, [ADR 0089].
//
// The slice is the instance's, with every **defined** entry replaced by a fresh `*global` and every
// **imported** entry left as the instance's own pointer. That is T-6's exclusion made structural rather
// than checked: an import names a cell the exporting instance owns, so a per-agent copy would answer a
// different question than the module asked, and because the distinction is settled *here* it costs
// nothing at `global.get`.
//
// **Initializers are re-evaluated rather than the current values copied**, which is what makes the
// semantics *fresh initialization* and not inheritance: a global declared `(global i32 (i32.const 7))`
// reads 7 in a spawned thread however many times the spawner has written it. Fresh-init is what a
// separate instance would give, which is where both upstream threading models converge, and it is the
// half of this decision an observer can falsify.
//
// **Evaluating through the host thread is correct for every module the validator admits**, and the
// reason is worth stating because it looks like a hole: a const-expr may read an *earlier global*, and
// `constExpr` runs on `&in.host`, so a fresh copy's initializer reads the **host thread's** globals
// rather than this thread's. A const-expr may only read an **immutable** global (`instr.go`'s
// `global.get` of an immutable), and an immutable global holds the same value in every thread, so the
// two readings coincide. For a module that is not validated they can differ — and this engine does not
// judge modules (`globalFor` declines the mutability check for that reason), so the behaviour there is
// unspecified rather than defended.
//
// Nil slots propagate as nil: a defined global whose initializer deferred at instantiation fails here
// too, and `spawn` refuses before the thread becomes a member of the world. An imported slot nothing
// supplied stays nil and `globalFor` reports it with the message it already has.
//
// [ADR 0089]: ../../docs/decisions/0089-non-shared-globals-are-per-agent-because-a-guests-per-thread-state-is-its-whole-global-set-and-t-4-sized-it-at-one.md
func (in *Instance) threadGlobals() ([]*global, error) {
	gs := make([]*global, len(in.globals))
	copy(gs, in.globals)
	off := in.mod.ImportedGlobals()
	for i := range in.mod.Globals {
		g, err := in.newGlobal(in.mod.Globals[i])
		if err != nil {
			return nil, fmt.Errorf("%w: a spawned thread's copy of global %d could not be initialized: %w",
				ErrThreadEntry, off+i, err)
		}
		gs[off+i] = g
	}
	return gs, nil
}

func (in *Instance) newGlobal(g binary.Global) (*global, error) {
	v, err := in.constExpr(g.Init, g.Type, "a global initializer")
	if err != nil {
		return nil, err
	}
	// Stored rather than set in the composite literal, because `atomic.Uint64` has no literal form.
	// **No lock is taken and none is needed**: nothing can reach this `*global` until `newGlobal`
	// returns it, so these two stores are construction, not shared mutation. That is decision 0058's
	// title premise arriving on a smaller object — reachability is the property that matters, and it
	// begins here rather than at spawn.
	out := &global{typ: g.Type, mutable: g.Mutable, mod: in.mod}
	out.num.Store(v.lo)
	out.numHi.Store(v.hi)
	// Unconditional for every shape, which is what makes `loadRef`'s missing nil check an invariant
	// rather than a bet: a numeric global never reads this slot, and it costs one 40-byte cell per
	// global to have every `*global` this package hands out satisfy the same precondition.
	out.storeRef(v.ref)
	return out, nil
}

// storeRef publishes r as this global's reference value.
//
// **The copy is the mechanism, not a defensive habit.** `r` is a parameter, so the cell this stores is
// reachable only through the pointer it publishes, and no caller — including the one whose value was
// copied — can write to what a reader is holding. Written as a method rather than as `g.ref.Store(&r)`
// at each call site so that the copy cannot be skipped at one of them by publishing an address the
// caller keeps: `g.ref.Store(p)` still compiles, and the reason to route through here is that a reviewer
// reading a call site sees a value being handed over rather than an address being shared.
func (g *global) storeRef(r ref) {
	g.ref.Store(&r)
}

// loadRef reads the published reference value.
//
// One load and one copy out of the cell it names, with nothing in between that could observe a second
// publication — the point of the mechanism. No nil check, on the invariant `newGlobal` establishes and
// its comment states.
func (g *global) loadRef() ref {
	return *g.ref.Load()
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
// **The thread is a parameter, because a global's storage is per-thread** — contract §2 T-6,
// [ADR 0089]. `t.globals` is indexed identically to `in.globals` (imports first, then definitions) and
// for the host thread it *is* `in.globals`, aliased at construction, so this resolves in exactly one
// indexing whichever thread asks. The import/definition distinction T-6 draws is settled once at
// spawn, not here: an imported slot in a spawned thread's slice is the instance's own pointer.
//
// The bounds and nil checks read `t.globals` while the *messages* quote the module's index space, which
// is the same space: a thread's slice is built at the instance's length and never resized.
//
// [ADR 0089]: ../../docs/decisions/0089-non-shared-globals-are-per-agent-because-a-guests-per-thread-state-is-its-whole-global-set-and-t-4-sized-it-at-one.md
func (in *Instance) globalFor(t *thread, what string, idx uint64) (*global, error) {
	// Per-thread storage applies only to the instance the thread's slice is indexed in — see
	// `thread.globalsOf` for the cross-instance call this guards and for the limit it leaves. One
	// pointer compare, and it is what keeps an imported function's body reading its *own* module's
	// index space.
	//
	// **Nil-tolerant, matching `poll`'s treatment of the same receiver** (`if t == nil || …`): a stack
	// with no thread is a test-only state this package already admits, and the fallback it lands on is
	// the instance's own globals — the pre-T-6 behaviour, which is the right default for a caller that
	// could not say which thread is asking. Established by a nil dereference here, not by taste:
	// `TestGlobalGetOfARefUsesTheRefStack` builds a bare `&stack{}` and reaches this line.
	gs := in.globals
	if t != nil && t.globalsOf == in {
		gs = t.globals
	}
	if idx >= uint64(len(gs)) {
		return nil, fmt.Errorf("%w: %s names global %d of %d",
			ErrNotValidated, what, idx, len(gs))
	}
	if gs[idx] == nil {
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
	return gs[idx], nil
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
		st.pushRef(g.loadRef())
	case shapeV128:
		// Both halves read under one acquisition, which is the point: two atomic loads with no lock
		// between them can take `numHi` from one `set` and `num` from the next, and a v128 assembled
		// from two different writes is a value neither of them wrote (decision 0063).
		g.mu.Lock()
		hi, lo := g.numHi.Load(), g.num.Load()
		g.mu.Unlock()
		st.pushV128(hi, lo)
	default:
		st.pushNum(g.num.Load())
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
		return fromRef(g.loadRef(), g.typ)
	case shapeV128:
		// The same one-acquisition read as `get`'s v128 arm, and it is repeated rather than shared for
		// the reason this function exists at all: routing the read through `get` would need a stack.
		g.mu.Lock()
		lo, hi := g.num.Load(), g.numHi.Load()
		g.mu.Unlock()
		return Value{Type: g.typ, Bits: lo, Hi: hi}
	default:
		return Value{Type: g.typ, Bits: g.num.Load()}
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
		// Popped before the publication for the v128 arm's reason one case down: the pop touches this
		// thread's own stack, so nothing is gained by widening the published-value window over it.
		g.storeRef(st.popRef())
	case g.typ == binary.V128:
		// **Two slots asked for as two, not as one twice.** `needNum(2)` is the whole underflow
		// question for a v128, and `popV128` returns (hi, lo) in that order — `pushV128`'s own
		// order — so `lo` is the stack's true top. Transposing the two here is a wrong answer no
		// arity check can see, which is why the destinations are named rather than positional.
		if err := st.needNum(2); err != nil {
			return err
		}
		//
		// Popped before the lock is taken, and both stores made under it. The pop touches the calling
		// thread's own stack and nothing else, so holding `mu` across it would widen the critical
		// section over work no other thread can observe.
		hi, lo := st.popV128()
		g.mu.Lock()
		g.numHi.Store(hi)
		g.num.Store(lo)
		g.mu.Unlock()
	default:
		if err := st.needNum(1); err != nil {
			return err
		}
		g.num.Store(st.popNum())
	}
	return nil
}
