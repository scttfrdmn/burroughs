package interp

import (
	"fmt"
	"strings"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The three call opcodes with arms, named for control.go's reason: a bare 0x10 in a switch arm is
// a byte and these are a family.
//
// **`return_call` (0x12) is deliberately absent from this block**, and its absence is the shape of
// the tail-call gate rather than an omission. `return_call_indirect` is here because it is the
// *indirect* half — the half that shares every line of its resolution with 0x11 — and 0x12 needs
// nothing this file has: it is `call` with the frame reused, which is #7's `Invoke`-side work.
// Both are gated by `gateTailCall` in the decoder, so on the default board neither reaches here at
// all; the gate is what decides, and the switch arm is what answers once it has.
const (
	opCall               = 0x10
	opCallIndirect       = 0x11
	opReturnCallIndirect = 0x13
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
	fn, ok := in.mod.DefinedFunc(idx)
	if !ok {
		if idx < uint32(in.mod.ImportedFuncs()) {
			// An imported function. If a supplier filled the slot the call **crosses into
			// that instance**; if not, this is still contract §3's gap, reported as the
			// engine gap it is rather than as a module fault (`tableFor`'s rule: nothing is
			// wrong with the module).
			return in.callImport(idx, st, depth)
		}
		// Past the end of the index space, which is #9's `unknown function`.
		return fmt.Errorf("%w: call names function %d of %d",
			ErrNotValidated, idx, in.mod.ImportedFuncs()+len(in.mod.Funcs))
	}
	ft, err := in.funcType(fn)
	if err != nil {
		return err
	}
	return in.invoke(fn, ft, st, depth)
}

// callImport calls a function that reached this instance through an import.
//
// **The crossing is a change of receiver and nothing else** — the operands stay on the caller's
// stack and the results come back onto it, exactly as for a module-local call, because
// `eval.ml`'s `Func.FuncInst` carries its own instance and `invoke` reads locals from the frame
// rather than from the caller. What changes is which instance the callee's `memory`,
// `global.get` and `call` resolve against, which is the entire content of linking at this layer.
//
// **`depth` is passed through unincremented, and that is deliberate.** The budget counts wasm
// *frames*, and resolving an import builds none: the frame arrives when the callee's own `invoke`
// increments. Incrementing here would make a chain of re-exported imports cost budget for
// re-exports, so two scripts with the same call graph and different export plumbing would exhaust
// at different depths. An import chain cannot cycle — a supplier is instantiated before its
// importer — so the pass-through cannot lose the bound.
func (in *Instance) callImport(idx uint32, st *stack, depth int) error {
	ext, err := in.importedFunc(idx)
	if err != nil {
		return err
	}
	return ext.fnInst.call(ext.fnIdx, st, depth)
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
func (in *Instance) invoke(fn *binary.Func, ft *binary.FuncType, st *stack, depth int) error {
	total := fn.TotalLocals() + uint64(len(ft.Params))
	if total > maxFrameLocals {
		// `Invoke`'s ceiling, reached from the inside. Same reading: an engine limit, not a
		// verdict and not a trap.
		return fmt.Errorf("%w: a called function declares %d locals, and this engine's frame ceiling is %d",
			ErrUnsupported, total, maxFrameLocals)
	}
	if err := st.needNum(len(ft.Params)); err != nil {
		return err
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
	locals := make([]uint64, total)
	for i := len(ft.Params) - 1; i >= 0; i-- {
		if ft.Params[i].IsRef() {
			return fmt.Errorf("%w: a called function takes %s as parameter %d",
				ErrUnsupportedOp, ft.Params[i], i)
		}
		locals[i] = st.popNum()
	}
	// The declared locals are already zero — `make` gives that — and zero is the correct default
	// for every numeric type (`default_value`). A ref-typed local is refused, as at the boundary,
	// because `ref{}` is *function 0* rather than null and nothing here would notice.
	var refErr error
	fn.EachLocal(func(idx uint32, vt binary.ValType) bool {
		if vt.IsRef() {
			refErr = fmt.Errorf("%w: a called function declares %s as local %d", ErrUnsupportedOp, vt, idx)
			return false
		}
		return true
	})
	if refErr != nil {
		return refErr
	}
	// **The callee's results must be exactly its arity, and the check is here rather than at the
	// boundary**, because a call's results become the caller's operands: a callee leaving scratch
	// behind would silently corrupt the caller's stack, where at the boundary `Invoke` merely
	// reports a mismatched count. `run`'s own `returnFrom` truncates on an explicit return, so the
	// case this catches is a body falling off its end with extra values — #9's arity question,
	// arriving late.
	base := len(st.num)
	if err := in.runFrame(fn, locals, st, len(ft.Results), depth+1); err != nil {
		return err
	}
	if got := len(st.num) - base; got != len(ft.Results) {
		return fmt.Errorf("%w: a called function declares %d results and left %d values on the stack",
			ErrNotValidated, len(ft.Results), got)
	}
	return nil
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
	// **Imm0 is the *type* index and Imm1 the *table* index, which is the reverse of how the text
	// reads them.** `encode.ml:275` is `op 0x11; idx y; idx x` where `x` is the table and `y` the
	// type, and `decode.ml:397` reads them back in that order — so the wire form puts the type
	// first. Measured rather than read off the grammar: `wat2wasm --enable-all` on
	// `(call_indirect 1 (type $t2) …)` with type 2 and table 1 emits `11 02 01`.
	typeIdx, tabIdx := ins.Imm0, ins.Imm1
	tab, err := in.tableFor("instruction", tabIdx)
	if err != nil {
		return err
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
		return needErr
	}
	i := tableAddr(tab, st.popNum())
	r, err := tab.load(i) // `undefined element i` when out of bounds
	if err != nil {
		return err
	}
	if r.Null {
		return uninitializedElem(i)
	}
	// **`target` is the instance the callee's body belongs to, and it is `in` for every
	// module-local function.** A table slot may name an imported function, and that function's
	// frame must run against *its own* instance — its memory, its globals, its tables — so the
	// receiver of `invoke` is resolved here rather than assumed. Getting this wrong is invisible
	// on the whole numeric corpus and wrong on `linking.wast`'s `Mt`/`Nt` pairs, where the callee
	// reads a global the caller does not have.
	target := in
	fn, ok := in.mod.DefinedFunc(r.Addr)
	if !ok {
		if r.Addr >= uint32(in.mod.ImportedFuncs()) {
			return fmt.Errorf("%w: table slot %d names function %d of %d",
				ErrNotValidated, i, r.Addr, in.mod.ImportedFuncs()+len(in.mod.Funcs))
		}
		// **Resolved here and then type-checked *below*, sharing the defined path's check.**
		// `call` names a function statically and the validator has agreed its signature; this
		// opcode names a *type index* and must compare it against the callee's actual type at
		// run time. So the import is turned into a `(instance, defined function)` pair and then
		// falls into the same comparison the defined case uses — one trap message, one
		// `sameFuncType` call, because two copies of a check the suite reads by substring are
		// two places to spell it differently.
		ext, ierr := in.importedFunc(r.Addr)
		if ierr != nil {
			return fmt.Errorf("%w (table slot %d)", ierr, i)
		}
		target = ext.fnInst
		fn, ok = target.mod.DefinedFunc(ext.fnIdx)
		if !ok {
			// Unreachable while `Export` resolves re-exported imports through to their
			// definer, which is the invariant `Instance.funcs` is documented to hold. Stated
			// as a reachable check rather than a panic, per grave 0003: this asserts a
			// property of a *sibling function*, and a future arm could falsify it silently.
			return fmt.Errorf("%w: table slot %d resolves to function %d of a supplier that does not define it",
				ErrNotValidated, i, ext.fnIdx)
		}
	}
	ft, err := target.funcType(fn)
	if err != nil {
		return err
	}
	want, err := in.declaredFuncType(typeIdx)
	if err != nil {
		return err
	}
	if !sameFuncType(ft, want) {
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
		return &Trap{Reason: fmt.Sprintf("indirect call type mismatch, expected %s but got %s",
			funcTypeString(want), funcTypeString(ft))}
	}
	if depth >= callBudget {
		return trapExhaustion
	}
	return target.invoke(fn, ft, st, depth)
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

// sameFuncType is structural equality over functypes — the MVP reduction of `Match.match_deftype`.
//
// Written out rather than `slices.Equal` twice for a reason that is about the *comparison* rather
// than the code: a `binary.ValType` is a byte, so equality here compares wire encodings, and two
// reference types that differ only in a heap-type index would compare equal. That cannot happen on
// the default board (no reference type reaches a functype the interpreter runs) and is a real gap
// under GC, which is the same shortfall the doc comment above declares.
func sameFuncType(a, b *binary.FuncType) bool {
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
