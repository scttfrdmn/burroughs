package interp

import (
	"fmt"
	"strings"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The six call opcodes, named for control.go's reason: a bare 0x10 in a switch arm is a byte and
// these are a family.
//
// **`return_call` (0x12) joined this block when 0026's mechanism landed, and its previous absence
// is worth reading rather than deleting.** The old comment said 0x12 "needs nothing this file has:
// it is `call` with the frame reused", which was true of the *arm* and false of the file — frame
// reuse turns out to need `call`'s resolution split from `call`'s entry, which is this file's
// subject and nothing else's. So the three tail opcodes are here beside their non-tail siblings
// because each one *is* its sibling's resolution with a different ending (`resolveCall`,
// `resolveCallIndirect`, `resolveCallRef` below, each with two callers).
//
// Three opcodes, two gates: `return_call`/`return_call_indirect` are `gateTailCall`'s and
// `call_ref`/`return_call_ref` are `gateGC`'s — the function references proposal folded into GC, so
// on the default board a `return_call_ref` and a `return_call` are declined by different gates.
// Two proposals reaching one switch is the normal case here; the constants are grouped by
// *mechanism* (all six resolve a callee, and three of them then enter it) and `gatemap.go` is where
// the proposal each belongs to is recorded.
const (
	opCall               = 0x10
	opCallIndirect       = 0x11
	opReturnCall         = 0x12
	opReturnCallIndirect = 0x13
	opCallRef            = 0x14
	opReturnCallRef      = 0x15
)

// callBudget is how deep this engine will nest calls before reporting exhaustion.
//
// **The reference's own number, and it is a *number* rather than a stack probe** — `flags.ml:9` is
// `let budget = ref 256`, decremented per frame in `eval.ml:1080` and checked in `:1114`. So the
// spec's own interpreter models stack overflow with a counter, and `assert_exhaustion` is written
// against a counter's behaviour: `call.wast:337`'s `runaway` recurses without bound and must report
// `call stack exhausted` in finite time, which a counter guarantees and a host-stack probe does not.
//
// **Not 256, and the difference is a measurement rather than a preference.** The reference's 256 is
// low enough that `fac.wast:102`'s `fac-rec 25` — 25 frames — passes with room, but it is also low
// enough to refuse programs the spec permits: nothing in the spec bounds recursion at 256, and a Go
// guest's own call depth routinely exceeds it (a recursive descent parser, `encoding/json`'s
// decoder). The thesis workload is the deciding argument (§1): Burroughs is Go's engine, and a
// ceiling tuned to the reference's test convenience would refuse ordinary Go programs. So the
// figure is chosen the way `maxFrameLocals` was — high enough that a module refused here was
// constructed to recurse without bound, low enough that the Go stack this recursion runs *on* does
// not overflow first.
//
// 10000 frames of `callFrame` is the bound that matters, and it is the *host* stack that sets it:
// `run` recurses into itself per call, so a wasm frame costs a Go frame, and Go's default goroutine
// stack grows to 1 GB on 64-bit. Measured rather than assumed — see
// TestCallStackExhaustionIsReportedNotCrashed, which runs `runaway` and asserts the trap arrives.
//
// It is deliberately **not** derived from `debug.SetMaxStack` or from probing SP: a ceiling that
// varies by host makes the engine's verdict depend on where it runs, which is `maxFrameLocals`'
// stated rule and the same one here.
const callBudget = 10000

// trapExhaustion is `assert_exhaustion`'s text — `eval.ml:1115`'s
// `Exhaustion.error e.at "call stack exhausted"`.
//
// **A Trap rather than a fourth sentinel, and the suite is what decides that.** Exhaustion is a
// distinct outcome in the reference (`Exhaustion` is its own exception, not `Trap`) and the wast
// grammar has its own directive for it — so a reading that made this `ErrUnsupported` would be
// claiming an engine gap where the spec has a defined result. What makes `Trap` right rather than
// merely convenient is that the harness matches `assert_exhaustion` by the same substring rule it
// matches `assert_trap` by, and the reference's own `Exhaustion.error` produces a value carrying
// exactly this string.
var trapExhaustion = &Trap{Reason: "call stack exhausted"}

// call invokes a defined function: the `Invoke` arm of `eval.ml:1117-1129`, minus the host-function
// case this phase has no linking for.
//
// # Why this recurses rather than pushing onto an explicit frame stack
//
// `run` calls this and this calls `run`, so a wasm frame is a Go frame. The explicit-stack
// alternative — a `[]callFrame` in `run` with the dispatch loop switching frames — is what a
// compiler-backed engine wants and is **the wrong shape for v0**, for the reason 0002 chose the
// giant switch: the recursion costs nothing per *instruction*, and the flat form's win is paid at
// every dispatch. `Invoke`'s own value-stack sizing is what makes the recursion cheap here — the
// stack is shared across frames, so a call allocates the locals and nothing else.
//
// It is stated rather than left implicit because v1's stack switching (contract §7) needs the
// explicit form: a continuation cannot be captured out of the Go stack. So this shape has a known
// expiry, and the expiry is a phase rather than a defect.
//
// # The stack is shared and the operands are already on it
//
// `eval.ml`'s `split n1 vs` takes the callee's parameters off the caller's operand stack, so the
// arguments are *in place* when this is entered: pop them into the callee's locals, run, and the
// results the callee leaves are on the same stack the caller reads. That is why `st` is passed
// rather than a fresh stack per frame, and it is also the one place a wrong reading is invisible on
// most vectors — a call taking zero arguments behaves identically either way, which is most calls
// in the numeric corpus.
func (in *Instance) call(idx uint32, st *stack, depth int) error {
	if depth >= callBudget {
		return trapExhaustion
	}
	target, fn, ft, err := in.resolveCall(idx)
	if err != nil {
		return err
	}
	return target.invoke(fn, ft, st, depth)
}

// resolveCall is `call`'s half that answers *which function* — the index lookup, the import
// crossing, and the callee's declared type — with no frame built and nothing entered.
//
// **Split from entry because a tail call needs exactly this half and nothing after it** (0026,
// #253). `eval.ml:282-284`'s `ReturnCall` arm literally `step`s the plain `Call` and re-tags the
// resulting `Invoke`, so resolution is *shared verbatim* between the tail and non-tail opcodes by
// the reference's own construction. Two callers, one place that knows how a callee is named — the
// same rule `invoke` states for how a frame is built, and `funcRefTarget`'s reason for existing.
//
// **The crossing is a change of receiver and nothing else** — the operands stay on the caller's
// stack and the results come back onto it, exactly as for a module-local call, because
// `eval.ml`'s `Func.FuncInst` carries its own instance and `invoke` reads locals from the frame
// rather than from the caller. What changes is which instance the callee's `memory`,
// `global.get` and `call` resolve against, which is the entire content of linking at this layer.
// The returned `*Instance` is that receiver, and every caller must use it rather than its own — a
// tail call to an import is the case where getting this wrong is silent, see tailcall.go.
//
// **The import crossing costs no depth, and it can no longer be spelled otherwise.** This used to
// be `callImport`, which took `depth` and passed it through unincremented on the stated ground
// that the budget counts wasm *frames* and resolving an import builds none. That reasoning was
// right and is now structural instead: resolution has no depth parameter at all, so the frame
// arrives when the callee's own `invoke` increments and nothing on this path can charge for
// re-export plumbing. An import chain cannot cycle — a supplier is instantiated before its
// importer — so the recursion terminates.
//
// An unfilled slot is contract §3's gap, reported as the engine gap it is rather than as a module
// fault (`tableFor`'s rule: nothing is wrong with the module); `importedFunc` renders it.
func (in *Instance) resolveCall(idx uint32) (*Instance, *binary.Func, *binary.FuncType, error) {
	fn, ok := in.mod.DefinedFunc(idx)
	if !ok {
		if idx < uint32(in.mod.ImportedFuncs()) {
			ext, err := in.importedFunc(idx)
			if err != nil {
				return nil, nil, nil, err
			}
			return ext.fnInst.resolveCall(ext.fnIdx)
		}
		// Past the end of the index space, which is #9's `unknown function`.
		return nil, nil, nil, fmt.Errorf("%w: call names function %d of %d",
			ErrNotValidated, idx, in.mod.ImportedFuncs()+len(in.mod.Funcs))
	}
	ft, err := in.funcType(fn)
	if err != nil {
		return nil, nil, nil, err
	}
	return in, fn, ft, nil
}

// importedFunc resolves an imported function index to whatever fills its slot.
//
// **The unfilled slot keeps its old *behaviour* verbatim** — an unlinked module degrades to
// exactly what it did before there was a linker — but not its old words. The draft of this
// comment argued for keeping the message too, so the 624-vector bucket would not split mid-drain,
// and that argument was sound and is now spent: the drain measured **624 → 13** under the old
// string, so the key has served its purpose, and holding `linking is not implemented` past that
// point would be the engine testifying to an absence that no longer exists (grave #36). See
// memoryFor, where the same swap is argued at length for the same four sites.
func (in *Instance) importedFunc(idx uint32) (*Extern, error) {
	if int(idx) >= len(in.funcs) || in.funcs[idx] == nil {
		return nil, fmt.Errorf("%w: function %d is an import nothing supplied (contract §3)",
			ErrUnsupported, idx)
	}
	return in.funcs[idx], nil
}

// invoke builds the callee's frame and runs it, the arguments coming off the shared stack.
//
// Split from `call` because `call_indirect` reaches it by a different route — it has the function
// and its type already, having resolved them through a table — and the frame-building half is
// identical. Two callers, one place that knows how a frame is built.
//
// **Through `enterFrame` rather than `runFrame` directly, which is 0026's trampoline (#253).** A
// callee that tail-calls returns a `*tailCall` sentinel instead of leaving results, and the loop
// that consumes it belongs to whoever owns the frame — here and in `run`, the two entry points, and
// nowhere else. The arity check below therefore still reads a *settled* stack: by the time
// `enterFrame` returns without a sentinel, whichever function finally left the results left exactly
// the results this call's `ft` declares, which is property 4's whole content (the declared results
// stay the original frame's).
func (in *Instance) invoke(fn *binary.Func, ft *binary.FuncType, st *stack, depth int) error {
	locals, err := buildFrame(fn, ft, st)
	if err != nil {
		return err
	}
	// **The callee's results must be exactly its arity, and the check is here rather than at the
	// boundary**, because a call's results become the caller's operands: a callee leaving scratch
	// behind would silently corrupt the caller's stack, where at the boundary `Invoke` merely
	// reports a mismatched count. `run`'s own `returnFrom` truncates on an explicit return, so the
	// case this catches is a body falling off its end with extra values — #9's arity question,
	// arriving late.
	//
	// **Two arrays, two deltas — checking only `st.num` reported every ref-typed result as
	// missing, unconditionally.** `table.get`/`ref.func`/`ref.null` had no arms until #7's
	// opcode-arm stream reached them, so nothing before that could call a function returning a
	// funcref/externref through `call` or `call_indirect` (both funnel through here) and observe
	// this: `table_get.wast`'s `is_null-funcref` is `ref.is_null (call $f3 …)`, where `$f3`
	// returns a `funcref` and left it correctly on `st.refs` — but `len(st.num)-base` read `0`
	// against a declared arity of `1` every time, regardless of what actually happened on the ref
	// side. Counting `ft.Results` by kind and checking each array against its own count is what
	// `#9`'s arity question actually asks; one array can be exactly right while the other is
	// wrong, and a shared counter cannot tell the two apart.
	numBase, refBase := len(st.num), len(st.refs)
	wantNum, wantRef := countByArray(ft.Results)
	if err := in.enterFrame(fn, locals, st, wantNum, wantRef, depth+1); err != nil {
		return err
	}
	if gotNum, gotRef := len(st.num)-numBase, len(st.refs)-refBase; gotNum != wantNum || gotRef != wantRef {
		return fmt.Errorf("%w: a called function declares %d results (%d numeric, %d reference) "+
			"and left %d numeric, %d reference values on the stack",
			ErrNotValidated, len(ft.Results), wantNum, wantRef, gotNum, gotRef)
	}
	return nil
}

// buildFrame pops the callee's arguments off the shared stack into a fresh frame — everything
// `invoke` does *before* entering, and nothing after.
//
// **Split from entry for 0026's second property (#253): a tail call builds the callee's frame
// exactly the way a plain call does, and this is that loop rather than a second copy of it.** The
// reference gets the sharing for free — `eval.ml:282-305` steps the plain opcode and re-tags the
// resulting `Invoke`, so `:1069`'s `take n vs0` runs once in the source — and a second copy here
// would be grave #105's shape a third time, this time with the v128 two-slot conversion and grave
// #246's null fill as the facts to re-derive wrongly.
//
// No receiver, `funcRefTarget`'s reason: building a frame reads the callee's own `fn`/`ft` and the
// shared stack, and touches no instance state at all. The *entering* is what needs an owner, and
// keeping that distinction in the signatures is what makes a cross-instance tail call hard to spell
// wrongly (`enterFrame`, tailcall.go).
func buildFrame(fn *binary.Func, ft *binary.FuncType, st *stack) (*frame, error) {
	total := fn.TotalLocals() + uint64(len(ft.Params))
	if total > maxFrameLocals {
		// `Invoke`'s ceiling, reached from the inside. Same reading: an engine limit, not a
		// verdict and not a trap.
		return nil, fmt.Errorf("%w: a called function declares %d locals, and this engine's frame ceiling is %d",
			ErrUnsupported, total, maxFrameLocals)
	}
	// **Both arrays' arity is checked before either is popped from**, for `needNum`/`needRef`'s
	// own reason repeated at a second call site: a shortfall is `type mismatch`, #9's verdict,
	// not something this package enforces past reporting the layering debt. Counted once via
	// countByArray (control.go) rather than inlined — the same split `blockArity` needs for a
	// block's parameters, and the reason a called function's params needed it is the identical
	// reason a block's do (#196/#197): a ref-typed parameter reaching here used to refuse
	// unconditionally, below, and that refusal is exactly what #197 lifts now that the frame has
	// somewhere to put the value.
	paramNum, paramRef := countByArray(ft.Params)
	if err := st.needNum(paramNum); err != nil {
		return nil, err
	}
	if err := st.needRef(paramRef); err != nil {
		return nil, err
	}
	// **The parameters come off the stack in reverse and land in declaration order**, which is
	// `eval.ml:1126`'s `List.(rev (map Option.some args) @ map default_value ts)`: `args` is
	// popped from the top, so reversing it puts parameter 0 in local 0. Filling forward instead
	// would swap every argument list of length ≥ 2 and be invisible on the 1-parameter majority.
	//
	// **This one is oracle-covered, and the figure is measured rather than assumed**: replacing
	// the loop with `for i := range ft.Params` moves the board 27451 → 27406 pass, 3682 → 3727
	// fail. So it gets no control of its own — #38's refinement is to read the vectors and know
	// which case you are in, and 45 of them fail on this. The rule the *sibling* facts needed
	// (§9 G-3, an accept-direction defect the suite scores green) does not apply here.
	//
	// **A ref-typed parameter now pops from `st.refs` rather than refusing (#196/#197)** — the
	// frame's own parallel array (`newFrame`, value.go), the same split 0002 pins for the value
	// stack, keyed by the same local index. A local's array is decided by its declared type
	// exactly as a stack slot's is (global.go's `get`/`set` give the identical reasoning), so
	// the frame's own isRef bitmap chooses which array both the write here and every `local.*`
	// arm in exec.go reads.
	//
	// **A `v128` parameter pops two slots, and popping one was grave #243.** Decision 0024 puts a
	// v128 in *two adjacent numeric stack slots* while the frame keeps it as one index across the
	// `num`/`numHi` pair — so this loop is the conversion between the two representations, and it
	// used to do the ref case, the numeric case, and nothing else. The consequence was worse than a
	// dropped high half: `popNum` left the caller's stack **one slot deep per v128 argument**, so
	// the *next* call read its own arguments from the leftovers. `simd_const.wast:1072-1074`
	// measures exactly that cascade — `$set_g0` answers `hi=0`, and `$set_g1_g2`, reading two slots
	// off a five-slot stack, gives `$g1` and `$g2` the *same* value, which is `$g2`'s high half.
	//
	// The sibling was right the whole time: `Invoke`'s boundary loop (interp.go) has carried the
	// `p == binary.V128` arm since 0024 landed, and this one re-derived the same conversion instead
	// of reading it — grave #105's shape, a lesson indexed by file rather than by shape. Both loops
	// now dispatch on the same three cases in the same order.
	//
	// **Oracle-covered, so it gets no control of its own** — the reverse-order fact above cites its
	// 45 vectors and declines a control for this reason, and the figure here is measured the same
	// way: deleting the `V128` case moves the default lane 142 → 150 fail, in three signatures (3
	// value mismatches, 4 `left 5 values`, 1 `left 3 values`). Both lanes, identically. §9 G-3's
	// rule — the one the *sibling* facts needed, an accept-direction defect the suite scores green
	// — does not apply to a defect eight vectors report.
	locals := newFrame(total, ft.Params, fn.EachLocal)
	for i := len(ft.Params) - 1; i >= 0; i-- {
		switch {
		case ft.Params[i].IsRef():
			locals.refs[i] = st.popRef()
		case ft.Params[i] == binary.V128:
			// Destinations named rather than positional, `global.set`'s v128 arm's reason:
			// `popV128` returns (hi, lo) because `pushV128`'s order makes lo the stack's true
			// top, and a transposition here is a wrong answer no arity check can see.
			locals.numHi[i], locals.num[i] = st.popV128()
		default:
			locals.num[i] = st.popNum()
		}
	}
	// The declared locals are already zero — `make` gives that — and zero is the correct default
	// for every numeric type (`default_value`). **A ref-typed local is not covered by `make`, and
	// `newFrame` writes its default explicitly: grave #246.**
	//
	// This comment used to say the opposite, and the way it was wrong is worth keeping. It
	// observed correctly that `locals.refs[i]`'s zero value is `ref{}` — i.e. `ref.func 0`, not
	// null — and then argued the gap was acceptable *because* `func.wast:662` and all three
	// `local_init.wast` vectors write such a local before reading it, so "the corpus never
	// exercises the uninitialized-ref-local default." That is a wrong answer defended on the
	// grounds that no vector reports it, which is the accept-direction reasoning §9 G-3 exists to
	// forbid, stated in the one place a reviewer checks code against claims: *the defect stated as
	// the rule*.
	//
	// The substantive error underneath the rhetorical one was a conflation of two cases the spec
	// keeps apart:
	//
	//   - A **nullable** ref-typed local has a default, and it is `ref.null` — `value.ml:150-152`'s
	//     `default_value` returns `NullRef` for a nullable reference type. So this needs an engine
	//     default and nothing from #9, which is the half the old comment denied.
	//   - A **non-nullable** ref-typed local has *no* default value, and validation must prove
	//     `local.set` precedes every `local.get`. That is genuinely #9's "uninitialized local"
	//     concept and `local_init.wast`'s actual subject, and it is still open.
	//
	// One case was closed and the other was not; citing the open one to excuse the closed one made
	// the whole paragraph read as a deferral. See `newFrame` for the fill and for why it covers
	// the parameter slots redundantly rather than wrongly.
	return locals, nil
}

// funcRefTarget resolves a non-null funcref to the `(instance, defined function)` pair that owns
// its body. `site` names where the reference came from, and appears verbatim in every error this
// returns.
//
// # Why this is a function rather than two copies
//
// `call_indirect` reads a funcref out of a table and `call_ref` pops one off the stack, and past
// that first step the resolution is identical — including the cross-instance subtlety below, which
// is a grave. Two copies would be two places for grave #163 to be reintroduced in one of them, and
// the copy that gets it wrong is invisible on the whole numeric corpus.
//
// **`site` is a pre-rendered string, not a format argument**, so that `call_indirect`'s three
// existing messages survive the extraction byte for byte: it passes `fmt.Sprintf("table slot %d",
// i)` and gets back exactly `table slot 3 names function 5 of 7`, the text the board's bucket keys
// were measured against. Confirmed rather than intended — the default lane is bit-identical across
// this refactor (58429/279/2689/3625/0 before and after). A `site string, idx int` pair would have
// been the natural signature and would have forced this function to know that a slot is numbered,
// which `call_ref`'s operand is not.
//
// # The cross-instance resolution, which is grave #163's
//
// **`target` is the instance the callee's body belongs to, resolved through `r.Inst` and never
// through the calling instance (grave #163, 0017 Q2).** A table slot may hold *another instance's*
// funcref — `r.Inst` is the instance whose index space `r.Addr` was read from when the element
// segment that filled this slot was evaluated, which is the caller for a module-local function and
// a different instance whenever this table was imported and someone else's segment wrote into it.
// Resolving `r.Addr` against the caller's module instead would land on whatever function it happens
// to have at that index — the exact defect the issue's synthetic reproducer isolates (a decoy
// function at the same index, `got 99, want 11`) and the exact vector `linking.wast:342-353` pins:
// `$Ot` writes its own functions into `$Mt`'s table, and `$Mt.call` must resolve them against
// `$Ot`, not against itself. Getting this right is invisible on the whole numeric corpus and wrong
// on any cross-instance table.
//
// The same argument makes this the right shape for `call_ref`, and there it is not even a subtlety:
// `ref.func` records `Inst` at the point the reference is *made* (`opRefFunc` pushes `ref{Addr:
// …, Inst: in}`), so a funcref that crossed an instance boundary through a global or a parameter
// carries its definer with it, and reading `Addr` against the *current* instance is the same bug
// with a shorter path to it.
func funcRefTarget(r ref, site string) (*Instance, *binary.Func, error) {
	target := r.Inst
	fn, ok := target.mod.DefinedFunc(r.Addr)
	if ok {
		return target, fn, nil
	}
	if r.Addr >= uint32(target.mod.ImportedFuncs()) {
		return nil, nil, fmt.Errorf("%w: %s names function %d of %d",
			ErrNotValidated, site, r.Addr, target.mod.ImportedFuncs()+len(target.mod.Funcs))
	}
	// **Resolved here and then type-checked by the *caller*, sharing the defined path's check.**
	// `call` names a function statically and the validator has agreed its signature;
	// `call_indirect` names a *type index* and must compare it against the callee's actual type at
	// run time. So the import is turned into a `(instance, defined function)` pair and then falls
	// into the same comparison the defined case uses — one trap message, one `sameFuncType` call,
	// because two copies of a check the suite reads by substring are two places to spell it
	// differently. `call_ref` performs no such comparison at all; see its arm for the reference's
	// reason (`CallRef _x` — the type immediate is unused).
	//
	// **`target.importedFunc`, not the caller's** — the import slot being resolved is the *naming*
	// instance's own import, one level of indirection past `r.Inst` when the funcref chains through
	// more than one `register`. `r.Inst` is the instance whose segment wrote this slot, and if that
	// instance's own function 0 is itself an import, its resolution lives in its own `funcs` slice.
	ext, ierr := target.importedFunc(r.Addr)
	if ierr != nil {
		return nil, nil, fmt.Errorf("%w (%s)", ierr, site)
	}
	target = ext.fnInst
	fn, ok = target.mod.DefinedFunc(ext.fnIdx)
	if !ok {
		// Unreachable while `Export` resolves re-exported imports through to their definer, which
		// is the invariant `Instance.funcs` is documented to hold. Stated as a reachable check
		// rather than a panic, per grave 0003: this asserts a property of a *sibling function*, and
		// a future arm could falsify it silently.
		return nil, nil, fmt.Errorf("%w: %s resolves to function %d of a supplier that does not define it",
			ErrNotValidated, site, ext.fnIdx)
	}
	return target, fn, nil
}

// callIndirect resolves a table slot to a function and calls it — `eval.ml:272-280`.
//
// # The three failures, in the reference's order
//
// They are three *different* outcomes and the order they are checked in is the order the reference
// checks them, because a vector exists for each and two of them are distinguishable only by which
// string arrives:
//
//  1. the index is past the table's end — `undefined element i` (`any_ref`, `:122-124`);
//  2. the slot holds null — `uninitialized element i` (`func_ref`, `:129`);
//  3. the slot's function has the wrong type — `indirect call type mismatch` (`:277-280`).
//
// Checking 2 before 1 would report `uninitialized element` for an out-of-bounds index on every
// table whose slots are null, which is *every table this engine builds* (newTable null-fills). So
// the ordering is not a stylistic matter: `call_indirect.wast` has 6 vectors wanting the first
// string and `elem.wast` 5 wanting the second, and a swapped pair fails both sets in opposite
// directions.
//
// # Why the type check compares functypes structurally
//
// `eval.ml:276` is `Match.match_deftype [] (Func.type_of f) (type_ c.frame.inst y)` — a *subtyping*
// test, not an index comparison. Comparing type indices would reject `call_indirect (type $a)` on a
// function declared with a structurally identical `$b`, which `type-rec.wast` has 2 vectors for and
// which is the accept direction: a valid module refused, invisible to a rejection corpus.
//
// It is a structural equality here rather than the full subtyping relation, and the difference is
// exactly GC's: `match_deftype` reduces to equality for MVP functypes, since the only subtyping
// among them is through GC's `sub` declarations. So this is right on the default board and is a
// declared shortfall in the all-gates-on lane — stated rather than left for a reader to discover,
// per *unreachability is a grave only when it's silent*.
func (in *Instance) callIndirect(ins binary.Instr, st *stack, depth int) error {
	target, fn, ft, err := in.resolveCallIndirect(ins, st)
	if err != nil {
		return err
	}
	if depth >= callBudget {
		return trapExhaustion
	}
	return target.invoke(fn, ft, st, depth)
}

// resolveCallIndirect is `callIndirect`'s half that answers *which function* — the table read, the
// three failures in the reference's order, and the structural type comparison — with no frame built
// and nothing entered. `resolveCall`'s reason, at the second of three sites: `eval.ml:286-292`
// re-tags the plain opcode's `Invoke`, so a tail call resolves through exactly this and then ends
// differently.
//
// **The budget check is *not* here, and its absence is load-bearing.** A tail call consumes no
// budget — `eval.ml:1080` decrements on `Frame` entry and `:1114` checks at `Invoke`, and a
// re-tagged `Invoke` arrives in the *parent's* instruction list (`:1072-1074`), so the frame that
// would have been charged is the one being replaced. Leaving the check at the two `invoke` call
// sites rather than lifting it into resolution is what keeps `return_call`'s own unbounded
// recursion (`return_call.wast`'s 1M-deep `even`/`odd`) from exhausting while `call.wast:337`'s
// `runaway` still does.
//
// The *placement* is load-bearing in that sense; a check merely **added** to a `return_call*` arm
// would be nearly inert, because `depth` does not grow along a tail chain. Measured: it costs
// nothing on any of the 141 tail-call vectors and shows up only on a tail call made from the
// deepest frame the budget permits, which is the row
// TestTailCallConsumesNoBudgetButNestingStillDoes adds for it.
func (in *Instance) resolveCallIndirect(ins binary.Instr, st *stack) (*Instance, *binary.Func, *binary.FuncType, error) {
	// **Imm0 is the *type* index and Imm1 the *table* index, which is the reverse of how the text
	// reads them.** `encode.ml:275` is `op 0x11; idx y; idx x` where `x` is the table and `y` the
	// type, and `decode.ml:397` reads them back in that order — so the wire form puts the type
	// first. Measured rather than read off the grammar: `wat2wasm --enable-all` on
	// `(call_indirect 1 (type $t2) …)` with type 2 and table 1 emits `11 02 01`.
	typeIdx, tabIdx := ins.Imm0, ins.Imm1
	tab, err := in.tableFor("instruction", tabIdx)
	if err != nil {
		return nil, nil, nil, err
	}
	// The index operand is an **i32 read unsigned**, and widening it to 64 bits before the bounds
	// test is what makes the test right: a table64's index is genuinely 64-bit
	// (`addr_of_num`), and truncating would wrap a large index into a legal slot.
	//
	// Through `tableAddr` rather than open-coding the two lines, which is what stood here: the
	// width decision belongs to one function or it belongs to two that can disagree, and
	// `table.copy` needing the same narrowing is what made the second copy visible. One concept,
	// one trigger — see `tableAddr` for why the i32 branch is an identity on every input this
	// engine can present, and why it is kept anyway.
	//
	// **The lines removed from here were unreachable too, and that was measured after the
	// retrofit rather than assumed by analogy.** Collapsing `tableAddr` to `return slot` leaves
	// both boards identical (29005/1961, exec 608) and this package green, so the narrowing that
	// stood at this call site was as unobservable as the one in `bulk.go` — for the same reason,
	// `pushI32` having already zero-extended the operand. The comment above about a large index
	// wrapping into a legal slot describes what would happen *if* a raw i64 could arrive here; it
	// cannot today, and memory64 is when that changes.
	if needErr := st.needNum(1); needErr != nil {
		return nil, nil, nil, needErr
	}
	i := tableAddr(tab, st.popNum())
	r, err := tab.load(i) // `undefined element i` when out of bounds
	if err != nil {
		return nil, nil, nil, err
	}
	if r.Null {
		return nil, nil, nil, uninitializedElem(i)
	}
	target, fn, err := funcRefTarget(r, fmt.Sprintf("table slot %d", i))
	if err != nil {
		return nil, nil, nil, err
	}
	ft, err := target.funcType(fn)
	if err != nil {
		return nil, nil, nil, err
	}
	want, err := in.declaredFuncType(typeIdx)
	if err != nil {
		return nil, nil, nil, err
	}
	// **`sameFuncType`'s own module and type index, not just its bare functype, as of 0019's
	// own named gap** — the declared-supertype walk climbs `target.mod.Types` starting from
	// the callee's own declared type (`fn.TypeIndex`), so it needs the *module* that index is
	// relative to, not only the functype `funcType` already resolved out of it. Argument order
	// matches the reference's own call (`Match.match_deftype [] (Func.type_of f) (type_
	// c.frame.inst y)`, eval.ml:274): the callee's actual type first, the call site's declared
	// type second — load-bearing, since only the first argument's supertype chain is walked.
	if !sameFuncType(target.mod, fn.TypeIndex, in.mod, uint32(typeIdx)) {
		// **The trap names both types because the reference's does** — `eval.ml:278-280` is
		// `"indirect call type mismatch, expected " ^ string_of_deftype … ^ " but got " ^ …` —
		// and *not* because a vector asks for it: the harness matches by substring and every
		// one of the 25 vectors stops at the sentinel (counted, see funcTypeString). So the tail
		// is ours alone to keep honest, which is why it is rendered from the functypes actually
		// compared rather than from the indices — the fabricated-evidence rule (grave #36): a
		// message naming a value must name the value the engine used.
		//
		// The first version of this comment said `call_indirect.wast:552` "wants it to". That
		// line is a `fib-i64` assert_return, and no vector in the corpus reads past the
		// sentinel; the citation was invented in the direction of claiming oracle cover for the
		// half of the message that has none, which is the reverse of the honest reading and the
		// exact thing #38's refinement exists to keep straight (grave #147).
		return nil, nil, nil, &Trap{Reason: fmt.Sprintf("indirect call type mismatch, expected %s but got %s",
			funcTypeString(want), funcTypeString(ft))}
	}
	return target, fn, ft, nil
}

// callRef is `call_ref` — `eval.ml:263-267`'s
// `Ref (FuncRef f) -> … call_func f` with `NullRef _ -> Trap "null function reference"`.
//
// The trap and the resolution are both shared: null goes to `trapNullFuncRef` (a *different* string
// from `ref.as_non_null`'s, the reference's own distinction — see refop.go), and the non-null path
// goes through `funcRefTarget`, so grave #163's cross-instance rule is obeyed here by construction
// rather than by a second author remembering it. A funcref that crossed an instance boundary through
// a global or a parameter is the *normal* case for `call_ref`, not the exotic one it is for
// `call_indirect`, so this arm is the one where getting `r.Inst` wrong would bite hardest.
//
// # No type check, and that is the reference's reading rather than a shortcut
//
// `call_ref` carries a type immediate and `eval.ml` ignores it: the arm is `CallRef _x`, underscore
// and all. Unlike `call_indirect`, whose table slot's type is unknown until run time, `call_ref`'s
// operand is *statically* typed `(ref null $x)`, so the validator has already established the match
// and there is nothing left to compare. So this arm reads no type at all — and the fabricated-check
// alternative would be worse than useless: it would spend a `sameFuncType` walk to answer a question
// #9 owns, and get its trap text from nowhere, the reference having no such trap to quote.
//
// **Measured, because the first version of this paragraph claimed something false.** I wrote that no
// vector in the two files asserts any mismatch text; there are 19 such strings. What is actually
// true is stronger and settles the question: all **15** `type mismatch` vectors across the two files
// (4 in `call_ref.wast`, 11 in `return_call_ref.wast` — each file's whole count) are
// `assert_invalid`, and **zero** are `assert_trap`. So the corpus itself says the check belongs to
// the validator, and an engine that trapped here would be answering a question the suite asks only
// of #9.
func (in *Instance) callRef(st *stack, depth int) error {
	target, fn, ft, err := resolveCallRef(st)
	if err != nil {
		return err
	}
	if depth >= callBudget {
		return trapExhaustion
	}
	return target.invoke(fn, ft, st, depth)
}

// resolveCallRef is `callRef`'s half that answers *which function* — pop the operand, trap on null,
// resolve through `funcRefTarget` — with no frame built and nothing entered. `resolveCall`'s reason,
// at the third of three sites (`eval.ml:295-305`).
//
// No receiver, and here it is not a style choice but the *fact*: `call_ref`'s callee is named
// entirely by the operand's own `r.Inst` (grave #163), so the calling instance contributes nothing
// to the resolution. `resolveCall` and `resolveCallIndirect` both need one — an index and a table
// index are relative to the naming module — and this one does not, which is worth being able to see
// in the signature. The budget check stays at the two entry points; see `resolveCallIndirect` for
// why that placement is load-bearing.
func resolveCallRef(st *stack) (*Instance, *binary.Func, *binary.FuncType, error) {
	if err := st.needRef(1); err != nil {
		return nil, nil, nil, err
	}
	r := st.popRef()
	if r.Null {
		return nil, nil, nil, trapNullFuncRef
	}
	// An exnref reaching here is #9's, for refEq's stated reason: `call_ref`'s operand is typed
	// `(ref null $x)` with `$x` a functype, so nothing under `exn` can arrive in a validated module,
	// and inventing a verdict in the accept direction is what §9 G-3 says the suite cannot see.
	if r.Exc != nil {
		return nil, nil, nil, fmt.Errorf(
			"%w: call_ref on an exception reference, which is not a function reference",
			ErrNotValidated)
	}
	target, fn, err := funcRefTarget(r, "call_ref operand")
	if err != nil {
		return nil, nil, nil, err
	}
	ft, err := target.funcType(fn)
	if err != nil {
		return nil, nil, nil, err
	}
	return target, fn, ft, nil
}

// declaredFuncType resolves a type index to a functype — `funcType`'s other half, reaching the
// type section by index rather than through a function.
//
// Both failures are #9's, and the second is reachable only in the all-gates-on lane, for
// `funcType`'s reason: `Module.Types` keeps struct and array slots so GC type indices do not shift,
// so a `call_indirect (type $s)` naming a struct is a module the decoder accepts.
func (in *Instance) declaredFuncType(idx uint64) (*binary.FuncType, error) {
	if idx >= uint64(len(in.mod.Types)) {
		return nil, fmt.Errorf("%w: call_indirect names type %d of %d",
			ErrNotValidated, idx, len(in.mod.Types))
	}
	ct := &in.mod.Types[idx]
	if ct.Kind != binary.CompFunc {
		return nil, fmt.Errorf("%w: call_indirect names type %d, which is a %s",
			ErrNotValidated, idx, ct.Kind)
	}
	return &ct.Func, nil
}

// sameFuncType is `match_deftype` (match.ml:151-155), MVP-reduced over function types alone —
// 0019's own named widening of what used to be pure structural equality.
//
// **The reference's three-way disjunct, verified line-for-line against match.ml:151-155:**
//
//	dt1 == dt2 ||                                                (* optimisation *)
//	let s = subst_of c in subst_deftype s dt1 = subst_deftype s dt2 ||
//	let SubT (_fin, uts1, _st) = unroll_deftype dt1 in
//	List.exists (fun ut1 -> match_heaptype c (UseHT ut1) (UseHT (Def dt2))) uts1
//
// Read right to left against what this engine can compute:
//
//   - **Disjunct 1** (`dt1 == dt2`, pointer identity) has no analogue here — this codebase has no
//     interned deftype identity, and it is an optimisation only, never load-bearing for a verdict
//     `subst_deftype` disagrees with. Dropped rather than approximated.
//   - **Disjunct 2** (`subst_deftype s dt1 = subst_deftype s dt2`) is *not* pure structural
//     equality on the comptype alone, and this is the widening's whole content: `subst_deftype`
//     canonicalizes a rec-group-relative index (`Rec i`) before comparing, so two types in
//     *different* rec groups whose supertype chains are shaped identically (same relative
//     position, same finality, same comptypes at every level) compare equal even though their
//     absolute type indices differ — `type-subtyping.wast`'s M3 pattern (`$f1`/`$g1` in one
//     script, `$f2`/`$g2` in another, each its own `rec` group, importing across them must
//     link). A comparison keyed on *this module's absolute indices* — which is what `Final` plus
//     `Supertypes`-as-plain-indices retains — cannot see that two absolute chains are the *same
//     relative shape*, only whether they are the *same chain object*. So what this function
//     actually computes for disjunct 2 is `Final` agreement plus **recursive** same-module-object
//     comparison of `Supertypes` chains: sound whenever the two sides are being asked about types
//     declared in the same rec-group shape reachable by walking supertypes with matching finality
//     and matching comptypes, and it is *silent* — reports unequal rather than a wrong equal —
//     when the two absolute chains diverge only in cross-module rec-group relabelling. Measured
//     against the corpus rather than assumed: `type-subtyping.wast`'s M10/M11 pair
//     (:746-758, :760-772) is exactly that divergence — `$f11`/`$f12` (M10) and `$f01`/`$f02` in
//     the presence of an *unrelated* sibling `$f11`/`$f12` naming the same shape (M11) — and
//     `TestSameFuncTypeCorpusScope` pins the finding that this reduction correctly does **not**
//     resolve that pair, rather than silently mis-resolving it.
//   - **Disjunct 3** (the declared-supertype walk, `List.exists … uts1`) is exactly what this
//     function widens into: `matchesDeclaredSupertype` below, walking `a`'s own `Supertypes`
//     chain and asking whether any entry names `b`'s type by disjunct-2 agreement.
//
// **`subst_deftype`'s full rec-group canonicalization (disjunct 2's general case) is out of
// scope, measured rather than assumed**: the decoder retains no rec-group boundary at all (no
// `RecGroup`/group-relative index anywhere in `binary.Module`), so implementing the general case
// would need a new retained fact this ADR's own scope (`sameFuncType`'s widening alone) does not
// license adding. What this function computes without it is disjunct 2 restricted to *identical
// absolute-index chains* (which subsumes plain structural equality when both sides declare no
// supertypes at all — the pre-existing MVP case, unchanged) plus disjunct 3 in full. Two of
// #164's four vectors (`type-subtyping.wast:602,610`, the `Final`-differing M2 pair) resolve
// under that scope; the other two (:752,:767, M10/M11) do not, and stay in the same bucket for
// the reason stated above — a genuine, cited, and tested scope boundary rather than a silent gap.
func sameFuncType(modA *binary.Module, idxA uint32, modB *binary.Module, idxB uint32) bool {
	return matchDeftype(modA, idxA, modB, idxB, nil)
}

// matchDeftype is match_deftype's disjuncts 2 and 3, over function types, with a visited set
// guarding the walk disjunct 3 performs — needed because #9 (the validator) does not exist yet,
// so nothing has checked `check_subtype_sub`'s forward-reference and finality rules
// (valid.ml:169-174) that would otherwise make a cyclic or self-referential declared-supertype
// chain invalid before this ever runs. A module the decoder accepts but the validator would not
// is exactly `binary.CompType`'s own documented layering debt (`declaredFuncType`'s two #9
// failures, above) — this is the same debt reached from a different direction, and the guard
// keeps a malformed cycle from looping this function forever rather than reporting a mismatch.
func matchDeftype(modA *binary.Module, idxA uint32, modB *binary.Module, idxB uint32, visited map[[2]uint32]bool) bool {
	ctA, ftA, okA := funcCompTypeAt(modA, idxA)
	ctB, ftB, okB := funcCompTypeAt(modB, idxB)
	if !okA || !okB {
		// #9's layering debt: an index naming a struct, an array, or nothing at all. Not this
		// function's verdict to invent — the caller already resolved both sides through
		// `funcType`/`declaredFuncType`, whose own errors report this, so reaching here with an
		// unresolvable index is unreachable on every call site this package has today. False
		// rather than a panic, per grave 0003: a property of a sibling function, and a future
		// call site could falsify it silently.
		return false
	}

	// Disjunct 2, restricted scope (see the doc comment above for exactly which restriction):
	// same finality and the same structural comptype is always sufficient, whether or not either
	// side declares supertypes — this is the pre-existing MVP case widened to also check Final,
	// never narrowed.
	if ctA.Final == ctB.Final && structFuncTypeEqual(ftA, ftB) {
		// Same absolute chain *so far*; still requires the declared supertypes themselves to
		// agree pairwise for the two deftypes to be the same rec-group-relative shape. Modules
		// with no declared supertypes at all (the original MVP case) have both sides empty and
		// this is trivially true.
		if len(ctA.Supertypes) == len(ctB.Supertypes) {
			same := true
			key := [2]uint32{idxA, idxB}
			if visited[key] {
				// Cycle guard: this pair is already being compared further up the call stack.
				// Reported as agreeing-so-far, matching the reference's own pointer-identity
				// optimisation (`dt1 == dt2`) for the case this engine cannot short-circuit on
				// object identity — a self-referential chain is #9's malformed input, not a
				// question this function can answer by non-termination.
				return true
			}
			if visited == nil {
				visited = map[[2]uint32]bool{}
			}
			visited[key] = true
			for i := range ctA.Supertypes {
				if !matchDeftype(modA, ctA.Supertypes[i], modB, ctB.Supertypes[i], visited) {
					same = false
					break
				}
			}
			if same {
				return true
			}
		}
	}

	// Disjunct 3: does any of A's own declared supertypes match B by this same relation?
	// `match_heaptype`'s `UseHT (Def dt), UseHT (Def dt2) -> match_deftype c dt dt2` reduces to
	// exactly this recursive call for the function-type case (the only heaptype form this MVP
	// reduction handles — struct/array subtyping is 0020's territory, untouched here).
	for _, sup := range ctA.Supertypes {
		if matchDeftype(modA, sup, modB, idxB, visited) {
			return true
		}
	}
	return false
}

// funcCompTypeAt resolves a type index to its CompType and FuncType, reporting false when the
// index is out of range or names a non-function comptype — the same two conditions
// `declaredFuncType`/`funcType` already check at their own call sites, restated here because
// this function's callers are internal to the walk and have no Instance to report through.
func funcCompTypeAt(mod *binary.Module, idx uint32) (*binary.CompType, *binary.FuncType, bool) {
	if int(idx) >= len(mod.Types) {
		return nil, nil, false
	}
	ct := &mod.Types[idx]
	if ct.Kind != binary.CompFunc {
		return nil, nil, false
	}
	return ct, &ct.Func, true
}

// structFuncTypeEqual is pure structural equality over functypes — the pre-0019 MVP reduction,
// kept as the innermost comparison `sameFuncType`'s widening is built on rather than being
// replaced by it.
//
// Written out rather than `slices.Equal` twice for a reason that is about the *comparison* rather
// than the code: `binary.ValType`'s `==` compares its three fields (kind, null, idx), so two
// reference types differing only in a heap-type index correctly compare unequal as of 0018's
// widening. Before it, `ValType` was a byte with nowhere to put an index, so this comparison
// could only ever see the sentinel and every GC reference type collapsed to one indistinguishable
// value.
func structFuncTypeEqual(a, b *binary.FuncType) bool {
	if len(a.Params) != len(b.Params) || len(a.Results) != len(b.Results) {
		return false
	}
	for i := range a.Params {
		if a.Params[i] != b.Params[i] {
			return false
		}
	}
	for i := range a.Results {
		if a.Results[i] != b.Results[i] {
			return false
		}
	}
	return true
}

// funcTypeString renders a functype for the mismatch trap, in the reference's own spelling:
// `func [i32] -> [i32]`, with an empty result type written `[]` rather than omitted.
//
// **The spelling is `types.ml`'s and it is not the wat one, which is what the first version of
// this function got wrong.** `string_of_deftype` on an MVP functype reduces through
// `DefT (RecT [st], 0l) → string_of_subtype (SubT (Final, [], ct)) → string_of_comptype`, whose
// functype arm (`types.ml:382-383`) is `"func " ^ string_of_resulttype ts1 ^ " -> " ^
// string_of_resulttype ts2`; and `string_of_resulttype` (`:361-362`) brackets unconditionally, so
// the empty functype renders `func [] -> []`. The old comment claimed `(func (param i32) (result
// i32))` and claimed empty clauses were *dropped* — the wat spelling, and wrong twice, because
// wat is what one reaches for when writing a functype by hand.
//
// # Why the reference's spelling rather than a wat one, when nothing checks either
//
// All 25 `indirect call type mismatch` vectors stop at the sentinel — 11 in `call_indirect.wast`,
// 7 in `type-subtyping.wast`, 4 in `return_call_indirect.wast`, 2 in `type-rec.wast`, 1 in
// `linking.wast` — so **the entire "expected … but got …" tail is un-oracle-covered by
// construction**, which is grave #36's territory: the half of a message the suite cannot read is
// the half nothing else will check. Given no oracle either way, the choice is between a local
// invention and an *external authority*, and the authority is checkable — see
// TestFuncTypeStringIsTheReferenceSpelling, which pins the algorithm including the empty case the
// old comment had backwards.
//
// It is deliberately the opposite call from `text.resolvedVal.String`, which renders wat and says
// so. That one is read by someone holding a `.wat` file and matched by nothing; this one is the
// tail of a spec trap, so agreeing with the interpreter that defines the trap is what makes it
// verifiable rather than merely plausible.
func funcTypeString(ft *binary.FuncType) string {
	var b strings.Builder
	b.WriteString("func ")
	writeResultType(&b, ft.Params)
	b.WriteString(" -> ")
	writeResultType(&b, ft.Results)
	return b.String()
}

// writeResultType is `string_of_resulttype` (`types.ml:361-362`): the brackets are
// unconditional and the separator goes *between* entries, so the empty vector is `[]`.
//
// Its own function because the reference has it as its own function and both sides of the arrow
// go through it — which is the whole reason `func [] -> []` is right, and is the fact the first
// version of this file got backwards in prose. One concept, one writer.
func writeResultType(b *strings.Builder, ts []binary.ValType) {
	b.WriteByte('[')
	for i, t := range ts {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(t.String())
	}
	b.WriteByte(']')
}
