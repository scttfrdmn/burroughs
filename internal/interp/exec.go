package interp

import (
	"fmt"
	"math"
	"math/bits"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// run executes a function body: the giant switch decision 0002 chose (Q2 Option A).
//
// # Why a switch and not a table of funcs
//
// 0002 measured it. A closure-per-instruction build was **21.5µs against the switch's
// 11.9µs** and allocated 72 B/op — `*frame` escapes into every closure — so the form that
// looks like a dispatch table is both slower and allocating on the workload §1 names. The
// switch is not a placeholder for a better dispatch; it is the measured winner, and
// `internal/interp/dispatchbench` keeps the reproducer so the next author does not re-derive
// the negative.
//
// # Why the body is walked by index and not by a cursor type
//
// `[]binary.Instr` **is** the program (0002 Q1 Option B): the decoder already produced the
// internal form, and there is no second lowering. So `pc` indexes the same slice a branch
// target will index when control flow lands — which is the reason the decoder emits a
// structural header *before* its nested block and retains END and ELSE. Nothing here
// branches yet, and the walk is nonetheless written against that indexing rather than
// against a slice it consumes, because the alternative would have to be rewritten by #7's
// successor.
//
// # What is deliberately absent
//
// No control flow, no memory, no globals, no calls, no SIMD, no references. Measured before
// being chosen: the 139-opcode numeric core makes **13671** of the suite's `assert_return`
// commands answerable, adding all of block/loop/if/br/br_if/br_table/return/call/call_indirect
// takes that to **13699**, `select` adds **zero**, and globals add **7**. The remaining ~38900
// are behind the text encoder's frontier and behind v128 and reference types, not behind this
// loop. The narrow set is what the measurement recommended, not what was easy.
//
// Every absent opcode is `ErrUnsupportedOp` naming its own bytes, so the board's fail bucket
// for this layer is keyed by opcode and reads as a work list.
//
// `results` is the arity of the implicit function-body label — what a `return`, and a branch to
// the outermost depth, truncates the stack to. Passed in rather than read from `fn.TypeIndex`
// because one caller has no type: `constExprValue` builds a `binary.Func` around an offset
// expression, whose zero TypeIndex would name a real and unrelated type. See returnFrom.
//
// # run versus runFrame
//
// `run` is the depth-zero entry point — the boundary, and the const-expression callers — and
// `runFrame` is the same loop with a call depth threaded through it. Two names rather than one with
// a `0` at every call site, because the depth is the *only* thing that distinguishes them and a
// literal zero at a boundary call reads as an accident: a caller passing 0 from inside a frame would
// reset the exhaustion budget, which is a bug no vector can see (the budget's whole purpose is a
// case that does not terminate).
func (in *Instance) run(fn *binary.Func, locals []uint64, st *stack, results int) error {
	return in.runFrame(fn, locals, st, results, 0)
}

// runFrame is `run` at a known call depth. See run for the loop's design; `depth` counts the frames
// below this one and is what `callBudget` bounds.
func (in *Instance) runFrame(fn *binary.Func, locals []uint64, st *stack, results, depth int) error {
	body := fn.Body
	// **Not `for pc := range len(body)`**, because a branch writes to `pc`: the arms below set it
	// to a target and let the `pc++` carry it forward, which is why this walk indexes the slice
	// instead of consuming it.
	//
	// It carried `//nolint:intrange` for exactly that reason and no longer needs one — the
	// assignments make `intrange` stop firing on its own, so `nolintlint` reported the directive
	// as unused. That is the honest end for a suppression: it was a claim about a design the code
	// could not yet demonstrate, and the code now demonstrates it. Removed rather than kept for
	// documentation, since a directive that suppresses nothing is a suppression wearing a
	// disguise; the prose above is where the reason belongs.
	//
	// ctrl is the control stack: one entry per *active* block, loop, or if. Nil until the
	// first structural instruction, so a straight-line body allocates nothing — which is
	// most bodies, and the reason this is not sized from the body the way the value stack is.
	var ctrl []label
	for pc := 0; pc < len(body); pc++ {
		ins := body[pc]
		if ins.Prefix != 0x00 {
			return unsupported(ins)
		}
		switch ins.Op {
		// ---- structured control flow ---------------------------------------------
		//
		// See control.go for label semantics, the loop-versus-block continuation split,
		// and why target resolution happens at block entry rather than at build time.

		case opBlock, opLoop:
			end, err := matchEnd(body, pc)
			if err != nil {
				return err
			}
			// `blockResults`, not `results`: this block's result count and the *function's*
			// (this method's parameter) are different facts, and one name for both is how a
			// branch to the outermost depth would come to truncate to a block's arity. Caught
			// by `govet`'s shadow check, which is the linter doing the job the spirit clause
			// reserves the suppression for — here the finding is a real ambiguity, not a
			// design fight.
			params, blockResults, err := in.blockArity(ins.Imm0)
			if err != nil {
				return err
			}
			// **A loop's continuation is its own header's successor and a block's is past
			// its END** — the one asymmetry in this construct. And the arity a *branch*
			// sees differs with it: branching to a loop re-enters it and so supplies its
			// parameters, while branching to a block leaves it and so yields its results.
			l := label{cont: end + 1, arity: blockResults}
			if ins.Op == opLoop {
				l = label{cont: pc, arity: params}
			}
			// The height excludes the operands the block itself consumes, so a branch
			// truncating to height+arity cannot eat its enclosing frame's values.
			if len(st.num) < params {
				return fmt.Errorf("%w: block takes %d parameters with %d values on the stack",
					ErrNotValidated, params, len(st.num))
			}
			l.height = len(st.num) - params
			ctrl = append(ctrl, l)

		case opIf:
			end, err := matchEnd(body, pc)
			if err != nil {
				return err
			}
			params, blockResults, err := in.blockArity(ins.Imm0)
			if err != nil {
				return err
			}
			if err := st.needNum(1); err != nil {
				return err
			}
			cond := st.popI32()
			if len(st.num) < params {
				return fmt.Errorf("%w: if takes %d parameters with %d values on the stack",
					ErrNotValidated, params, len(st.num))
			}
			// The label is pushed for **both** arms and for the no-else case, because `br 0`
			// inside either arm exits the whole `if`. Its continuation is past the END, like
			// a block's: an `if` is not re-enterable.
			ctrl = append(ctrl, label{cont: end + 1, arity: blockResults, height: len(st.num) - params})
			if cond != 0 {
				break // fall into the then-arm
			}
			// False, so jump to the else-arm if there is one. `elseOf` matches only at
			// depth 1, so a nested `if`'s ELSE cannot be mistaken for this one's.
			if els, ok := elseOf(body, pc, end); ok {
				pc = els // the loop's pc++ lands on the first instruction of the else-arm
				break
			}
			// No else-arm: an `if` without one yields nothing, so the whole construct is
			// skipped and its label popped — the END that would pop it is never reached.
			ctrl = ctrl[:len(ctrl)-1]
			pc = end

		case opElse:
			// Reached only by *falling out of* a then-arm that ran to completion, never by
			// the jump above — which lands past the ELSE. So this is the then-arm's exit,
			// and it must skip the else-arm rather than execute both.
			//
			// **The label is popped here, because the END that would pop it is jumped over.**
			// Same shape as opIf's no-else path: `cont` is *past* the END, so landing there
			// means the END never executes. This arm previously said the opposite — "the
			// label stays on the control stack: the END past the else-arm is what pops it" —
			// which described a mechanism the same line then skipped, and left one label per
			// taken then-arm on the stack. The function's own terminating END then saw a
			// non-empty `ctrl`, took itself for a block's END, and the body ran off its end:
			// `function body ended without END` on a valid module, an accept-direction
			// defect (grave #134).
			//
			// It survived 20 of 22 rows of the control written for it because the *encoder*
			// refused them all; the two rows that could reach it are exactly the two with a
			// taken then-arm and an else-arm present. Which is the falsifiability law's own
			// blind spot — a green that survives the bug it names — cleared by the artifact
			// the control was waiting on rather than by more control.
			if len(ctrl) == 0 {
				return fmt.Errorf("%w: ELSE outside any if", ErrNotValidated)
			}
			pc = ctrl[len(ctrl)-1].cont - 1 // cont is past the END; pc++ restores it
			ctrl = ctrl[:len(ctrl)-1]

		case opBr:
			// **Label `len(ctrl)` is the function body itself, and it is a return.** The
			// spec makes a function body an implicit block with the function's result type,
			// so `br 0` in a body with no enclosing block is legal and means return — a
			// reading that has to be here rather than in `branch`, because `ctrl` holds only
			// the *explicit* labels and the implicit one has no entry to look up. An earlier
			// draft of this arm invented a helper to answer the same question from the
			// wrong end (was the level zero?), which is a different question and gets `br 0`
			// inside one enclosing block wrong.
			if ins.Imm0 == uint64(len(ctrl)) {
				return returnFrom(st, results)
			}
			target, level, err := in.branch(st, ctrl, ins.Imm0)
			if err != nil {
				return err
			}
			ctrl = ctrl[:level]
			pc = target - 1 // the loop's pc++ lands on the target

		case opBrIf:
			if err := st.needNum(1); err != nil {
				return err
			}
			if st.popI32() == 0 {
				break // not taken: fall through, and the operand is already consumed
			}
			if ins.Imm0 == uint64(len(ctrl)) {
				return returnFrom(st, results) // taken, and the target is the function body — see opBr
			}
			target, level, err := in.branch(st, ctrl, ins.Imm0)
			if err != nil {
				return err
			}
			ctrl = ctrl[:level]
			pc = target - 1

		case opBrTable:
			// The operand is an **unsigned** index into the label vector, and out of range is
			// the default rather than a trap: `eval.ml:298-301` is
			// `if I32.ge_u i (Lib.List32.length xs) then … x else … (Lib.List32.nth xs i)`.
			// Reading it as signed would send a negative index into the vector's tail.
			if err := st.needNum(1); err != nil {
				return err
			}
			i := uint32(st.popNum())
			depth := ins.Imm0 // the default target, which br_table stages first (0016)
			if labels, ok := fn.LabelVector(pc); ok && i < uint32(len(labels)) {
				depth = uint64(labels[i])
			}
			// **The operand is popped before the branch, and that ordering is the spec's.**
			// `br_table`'s stack surgery is a plain `br` to the selected label *after* the
			// index is consumed, so the label's arity counts what is left below it. Popping
			// after would let the index survive as one of the block's results on any label
			// whose arity is non-zero.
			if depth == uint64(len(ctrl)) {
				return returnFrom(st, results) // the function body, as in opBr
			}
			target, level, err := in.branch(st, ctrl, depth)
			if err != nil {
				return err
			}
			ctrl = ctrl[:level]
			pc = target - 1

		case opReturn:
			// The results are on top of the stack in order, and everything below them is
			// scratch this frame is done with — so the stack is truncated to the function's
			// arity, which is what `eval.ml:1069`'s `take n vs0` does. See returnFrom for the
			// arm this replaces and for what its comment asserted (grave #135).
			return returnFrom(st, results)

		// ---- calls ---------------------------------------------------------------
		//
		// See call.go for the frame-building half, the exhaustion budget, and why
		// `call_indirect`'s three failures are checked in the reference's order.

		case opCall:
			if err := in.call(uint32(ins.Imm0), st, depth); err != nil {
				return err
			}

		case opCallIndirect:
			if err := in.callIndirect(ins, st, depth); err != nil {
				return err
			}

		case opReturnCallIndirect:
			// **A tail call, and this arm does not make it one.** The reference's
			// `ReturnCallIndirect` (`eval.ml:298-305`) steps a plain `CallIndirect` and then
			// wraps the result in `ReturningInvoke`, which replaces the current frame instead
			// of nesting under it. Here the call nests and the frame then returns, which is
			// *observationally identical* for every vector except one class: unbounded tail
			// recursion, which the spec requires to run forever and this arm exhausts.
			//
			// Stated rather than left silent because the difference is invisible on the
			// board — `return_call_indirect.wast` has no unbounded-recursion vector, so all 42
			// rows pass either way — and a proper tail call needs the explicit frame stack
			// `call`'s comment defers to v1. That is the shape of a declared shortfall: the
			// gate is off by default, so nothing reaches here unless the all-gates-on lane
			// puts it here, and when it does it answers on the merits for everything the suite
			// asks.
			if err := in.callIndirect(ins, st, depth); err != nil {
				return err
			}
			return returnFrom(st, results)

		case opEnd:
			// **Two meanings, and the control stack is what tells them apart** — which is
			// why the arm that used to say "in this opcode set END can only be the
			// function's terminator" is gone: that was true only while nothing structural
			// existed, and it said so.
			if len(ctrl) == 0 {
				return nil // the function's own terminating END
			}
			// A block's END, reached by falling out of its body rather than by branching.
			// The block's results are already on the stack; nothing to move.
			ctrl = ctrl[:len(ctrl)-1]

		case 0x00: // unreachable
			return trapUnreachable

		case 0x01: // nop

		case 0x1a: // drop
			if err := st.needNum(1); err != nil {
				return err
			}
			st.popNum()

		case opSelect, opSelectT:
			// **Both encodings, one arm, and the reason is that the difference is entirely a
			// validation-time one.** `0x1c` carries a `vec valtype` naming the operand type, and
			// `eval.ml:193-197` matches `Select _` — the wildcard is the reference discarding the
			// annotation, picking by the condition and nothing else. It exists so the validator
			// can type a `select` over reference operands, which is not decidable from the stack.
			// So an arm keying on the opcode would be inventing a distinction the reference does
			// not have.
			//
			// It is also the arm the decoder could not support otherwise: `immVecValType` reads
			// the types and drops them (instr.go), so `Instr` does not carry the annotation and
			// this arm could not branch on it if it wanted to.
			//
			// **Numeric slots only, and that is a real restriction rather than a simplification.**
			// A `select` over `externref` operands moves values on `st.refs`, which is empty
			// throughout v0 — no reference opcode exists to have put anything there — so such a
			// module cannot reach here with operands to move. When the first reference opcode
			// lands (#7's successor) this arm needs the refs case, and it will be reachable then;
			// stated here because a silent numeric-only select is the accept-direction shape.
			if err := st.needNum(3); err != nil {
				return err
			}
			cond := st.popI32()
			b := st.popNum()
			a := st.popNum()
			// `if v then v1 else v2` with the *first* operand as the true arm — the two are
			// popped in reverse, so `a` is the one written first in the text.
			if cond != 0 {
				st.pushNum(a)
			} else {
				st.pushNum(b)
			}

		// ---- locals --------------------------------------------------------------
		//
		// Imm0 is the local index, staged by immIdx. Out-of-range is #9's verdict
		// reported as the layering debt it is; the validator makes these three checks
		// unreachable.

		case 0x20: // local.get
			if ins.Imm0 >= uint64(len(locals)) {
				return badLocal(ins.Imm0, len(locals))
			}
			st.pushNum(locals[ins.Imm0])

		case 0x21: // local.set
			if ins.Imm0 >= uint64(len(locals)) {
				return badLocal(ins.Imm0, len(locals))
			}
			if err := st.needNum(1); err != nil {
				return err
			}
			locals[ins.Imm0] = st.popNum()

		case 0x22: // local.tee
			if ins.Imm0 >= uint64(len(locals)) {
				return badLocal(ins.Imm0, len(locals))
			}
			if err := st.needNum(1); err != nil {
				return err
			}
			// Peek, not pop-then-push: tee leaves the value on the stack.
			locals[ins.Imm0] = st.num[len(st.num)-1]

		// ---- constants -----------------------------------------------------------
		//
		// **Three arms push Imm0 unexamined; i32.const truncates, and the split is the
		// grave.** For i64/f32/f64 the decoder's staging is already the slot: an f32.const
		// is verbatim little-endian *bits*, never routed through a Go float, because a
		// signalling NaN's payload does not survive that round trip and the suite asserts
		// exact payloads.
		//
		// i32.const is the exception because `immS32` stages it **sign-extended to 64 bits**
		// (instr.go), while an i32 slot is defined as the low 32 bits with the high bits
		// *zero* — `i32.const -1` is `0xFFFFFFFF`, not `0xFFFFFFFFFFFFFFFF`. So the two
		// representations differ on every negative constant, and this arm is where the
		// conversion belongs.
		//
		// Grave #125. All four opcodes shared one arm, so `i32.const -1` reached the host
		// boundary as `0xFFFFFFFFFFFFFFFF`, and survived a `local.set`/`local.get` round trip
		// in that state, because both copy the raw slot. It did **not** corrupt
		// `i64.extend_i32_u`, which is what the first draft of this comment claimed: that path
		// is `pushI64(int64(uint32(popI32())))` and `popI32` truncates, so every route through
		// the `popI32`/`pushI32` helpers was protected, which is exactly where the damage
		// stopped. The claim was a prediction about a mechanism; reintroducing the defect
		// measured it (3 of `TestI32ConstOccupiesItsSlotZeroExtended`'s 6 rows fail, and
		// extend_i32_u is not one of them) and the prediction was wrong.
		//
		// **Invisible on the board by construction**: 114 of the corpus's
		// 6498 distinct const spellings were wrong and the pass count did not move, because
		// the *expectation* is read by `spec.readIntLit` — which was right — while the only
		// vectors that would have compared them were already failing on #8's encoder
		// frontier. What found it was `TestHarnessAndEngineLiteralReadersAgree`, the
		// second-opinion cross-check, on its first run. The engine agreeing with itself is
		// not evidence; the two readers disagreeing is.
		//
		// The tell was in the comment that used to sit here: it asserted the decoder staged
		// i32.const sign-extended *and* that pushing Imm0 unexamined was correct, which
		// cannot both hold. A comment stating the property the code lacks makes review
		// confirm the bug.

		case 0x41: // i32.const
			st.pushI32(int32(ins.Imm0))
		case 0x42, 0x43, 0x44: // i64.const, f32.const, f64.const
			st.pushNum(ins.Imm0)

		// ---- i32 comparisons -----------------------------------------------------

		case 0x45: // i32.eqz
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushBool(st.popI32() == 0)
		case 0x46, 0x47, 0x48, 0x49, 0x4a, 0x4b, 0x4c, 0x4d, 0x4e, 0x4f:
			if err := st.needNum(2); err != nil {
				return err
			}
			b := st.popI32()
			a := st.popI32()
			st.pushBool(cmpI32(ins.Op, a, b))

		// ---- i64 comparisons -----------------------------------------------------

		case 0x50: // i64.eqz
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushBool(st.popI64() == 0)
		case 0x51, 0x52, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5a:
			if err := st.needNum(2); err != nil {
				return err
			}
			b := st.popI64()
			a := st.popI64()
			st.pushBool(cmpI64(ins.Op, a, b))

		// ---- float comparisons ---------------------------------------------------
		//
		// Go's `<`, `>`, `<=`, `>=`, `==` on float32/float64 are IEEE 754 already: every
		// one is false when either operand is a NaN, and `-0.0 == +0.0` is true. Those
		// are the two behaviours wasm specifies, so these arms are the one place in this
		// file where the native operator is the whole implementation. `Value.Equal` is
		// the opposite case for the opposite reason — see there.

		case 0x5b, 0x5c, 0x5d, 0x5e, 0x5f, 0x60: // f32.eq … f32.ge
			if err := st.needNum(2); err != nil {
				return err
			}
			b := st.popF32()
			a := st.popF32()
			st.pushBool(cmpF32(ins.Op, a, b))

		case 0x61, 0x62, 0x63, 0x64, 0x65, 0x66: // f64.eq … f64.ge
			if err := st.needNum(2); err != nil {
				return err
			}
			b := st.popF64()
			a := st.popF64()
			st.pushBool(cmpF64(ins.Op, a, b))

		// ---- i32 arithmetic ------------------------------------------------------

		case 0x67: // i32.clz
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushI32(int32(bits.LeadingZeros32(uint32(st.popI32()))))
		case 0x68: // i32.ctz
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushI32(int32(bits.TrailingZeros32(uint32(st.popI32()))))
		case 0x69: // i32.popcnt
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushI32(int32(bits.OnesCount32(uint32(st.popI32()))))
		case 0x6a, 0x6b, 0x6c, 0x6d, 0x6e, 0x6f, 0x70, 0x71, 0x72, 0x73,
			0x74, 0x75, 0x76, 0x77, 0x78:
			if err := st.needNum(2); err != nil {
				return err
			}
			b := st.popI32()
			a := st.popI32()
			v, err := binI32(ins.Op, a, b)
			if err != nil {
				return err
			}
			st.pushI32(v)

		// ---- i64 arithmetic ------------------------------------------------------

		case 0x79: // i64.clz
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushI64(int64(bits.LeadingZeros64(uint64(st.popI64()))))
		case 0x7a: // i64.ctz
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushI64(int64(bits.TrailingZeros64(uint64(st.popI64()))))
		case 0x7b: // i64.popcnt
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushI64(int64(bits.OnesCount64(uint64(st.popI64()))))
		case 0x7c, 0x7d, 0x7e, 0x7f, 0x80, 0x81, 0x82, 0x83, 0x84, 0x85,
			0x86, 0x87, 0x88, 0x89, 0x8a:
			if err := st.needNum(2); err != nil {
				return err
			}
			b := st.popI64()
			a := st.popI64()
			v, err := binI64(ins.Op, a, b)
			if err != nil {
				return err
			}
			st.pushI64(v)

		// ---- f32 arithmetic ------------------------------------------------------
		//
		// abs, neg and copysign are **bit operations on the raw word**, not float
		// arithmetic, and they are separated from the rest for that reason: the spec
		// defines them on the sign bit alone, so a NaN's payload survives unchanged and
		// `f32.wast` asserts exact patterns like `nan:0x200000` for all three. Routing
		// them through the canonicalizing path below would answer `nan:canonical` to a
		// vector that named a payload.

		case 0x8b: // f32.abs
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushNum(uint64(uint32(st.popNum()) & ^signMask32))
		case 0x8c: // f32.neg
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushNum(uint64(uint32(st.popNum()) ^ signMask32))
		case 0x98: // f32.copysign
			if err := st.needNum(2); err != nil {
				return err
			}
			b := uint32(st.popNum())
			a := uint32(st.popNum())
			st.pushNum(uint64(a&^signMask32 | b&signMask32))
		case 0x8d, 0x8e, 0x8f, 0x90, 0x91: // ceil, floor, trunc, nearest, sqrt
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushF32(canon32(unF32(ins.Op, st.popF32())))
		case 0x92, 0x93, 0x94, 0x95, 0x96, 0x97: // add, sub, mul, div, min, max
			if err := st.needNum(2); err != nil {
				return err
			}
			b := st.popF32()
			a := st.popF32()
			st.pushF32(canon32(binF32(ins.Op, a, b)))

		// ---- f64 arithmetic ------------------------------------------------------

		case 0x99: // f64.abs
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushNum(st.popNum() & ^signMask64)
		case 0x9a: // f64.neg
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushNum(st.popNum() ^ signMask64)
		case 0xa6: // f64.copysign
			if err := st.needNum(2); err != nil {
				return err
			}
			b := st.popNum()
			a := st.popNum()
			st.pushNum(a&^signMask64 | b&signMask64)
		case 0x9b, 0x9c, 0x9d, 0x9e, 0x9f: // ceil, floor, trunc, nearest, sqrt
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushF64(canon64(unF64(ins.Op, st.popF64())))
		case 0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5: // add, sub, mul, div, min, max
			if err := st.needNum(2); err != nil {
				return err
			}
			b := st.popF64()
			a := st.popF64()
			st.pushF64(canon64(binF64(ins.Op, a, b)))

		// ---- conversions ---------------------------------------------------------

		case 0xa7: // i32.wrap_i64
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushI32(int32(uint32(st.popI64())))
		case 0xac: // i64.extend_i32_s
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushI64(int64(st.popI32()))
		case 0xad: // i64.extend_i32_u
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushI64(int64(uint32(st.popI32())))

		case 0xa8, 0xa9, 0xaa, 0xab: // i32.trunc_f{32,64}_{s,u}
			if err := st.needNum(1); err != nil {
				return err
			}
			v, err := truncToI32(ins.Op, st)
			if err != nil {
				return err
			}
			st.pushI32(v)
		case 0xae, 0xaf, 0xb0, 0xb1: // i64.trunc_f{32,64}_{s,u}
			if err := st.needNum(1); err != nil {
				return err
			}
			v, err := truncToI64(ins.Op, st)
			if err != nil {
				return err
			}
			st.pushI64(v)

		case 0xb2: // f32.convert_i32_s
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushF32(float32(st.popI32()))
		case 0xb3: // f32.convert_i32_u
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushF32(float32(uint32(st.popI32())))
		case 0xb4: // f32.convert_i64_s
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushF32(float32(st.popI64()))
		case 0xb5: // f32.convert_i64_u
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushF32(float32(uint64(st.popI64())))
		case 0xb6: // f32.demote_f64
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushF32(canon32(float32(st.popF64())))
		case 0xb7: // f64.convert_i32_s
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushF64(float64(st.popI32()))
		case 0xb8: // f64.convert_i32_u
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushF64(float64(uint32(st.popI32())))
		case 0xb9: // f64.convert_i64_s
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushF64(float64(st.popI64()))
		case 0xba: // f64.convert_i64_u
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushF64(float64(uint64(st.popI64())))
		case 0xbb: // f64.promote_f32
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushF64(canon64(float64(st.popF32())))

		// ---- reinterpret ---------------------------------------------------------
		//
		// **Four arms that do nothing, and the nothing is the semantics.** A reinterpret
		// is a type-level assertion over an unchanged bit pattern, and 0002's bare-uint64
		// slot already holds the f32 bits in the low 32 and the i32 bits in the same
		// place. So the correct implementation is to leave the word alone — and it is
		// written out rather than folded into the default arm precisely because an
		// implementation that looks like a mistake needs a comment saying it is not one.
		//
		// The i32/f32 pair carries one real obligation: the word must stay 32-bit clean.
		// It does, because pushI32 and pushF32 both zero the high half — so nothing is
		// masked here, and the reason nothing is masked is written down instead.

		case 0xbc, 0xbd, 0xbe, 0xbf:
			if err := st.needNum(1); err != nil {
				return err
			}

		// ---- sign extension -----------------------------------------------------

		case 0xc0: // i32.extend8_s
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushI32(int32(int8(st.popI32())))
		case 0xc1: // i32.extend16_s
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushI32(int32(int16(st.popI32())))
		case 0xc2: // i64.extend8_s
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushI64(int64(int8(st.popI64())))
		case 0xc3: // i64.extend16_s
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushI64(int64(int16(st.popI64())))
		case 0xc4: // i64.extend32_s
			if err := st.needNum(1); err != nil {
				return err
			}
			st.pushI64(int64(int32(st.popI64())))

		// ---- linear memory -------------------------------------------------------
		//
		// Imm0 is the static offset and Imm1 the memory index, staged by decodeMemop;
		// alignment is deliberately not retained, being a validation constraint with no
		// execution semantics. The width, signedness and slot type come from `memops`,
		// whose rows are cross-checked against the generated table's mnemonics rather
		// than hand-asserted (memop.go).

		case 0x28, 0x29, 0x2a, 0x2b, 0x2c, 0x2d, 0x2e, 0x2f,
			0x30, 0x31, 0x32, 0x33, 0x34, 0x35,
			0x36, 0x37, 0x38, 0x39, 0x3a, 0x3b, 0x3c, 0x3d, 0x3e:
			if err := in.memAccess(ins, st); err != nil {
				return err
			}

		case 0x3f: // memory.size
			mem, err := in.memoryFor("instruction", ins.Imm0)
			if err != nil {
				return err
			}
			// The result's *type* follows the memory's address width: an i64 memory's
			// size is an i64. `addrtype_of` is what the reference consults, which is
			// why Limits.Addr64 is retained.
			if mem.limits.Addr64 {
				st.pushI64(int64(mem.size()))
			} else {
				st.pushI32(int32(uint32(mem.size())))
			}

		case 0x40: // memory.grow
			mem, err := in.memoryFor("instruction", ins.Imm0)
			if err != nil {
				return err
			}
			if err := st.needNum(1); err != nil {
				return err
			}
			// **Failure is -1 in the result, not a trap.** memory.grow is total: the
			// reference's SizeOverflow and SizeLimit become the sentinel value, so
			// returning an error here would convert ~53 assert_return vectors into
			// assert_trap answers — the right failure reported on the wrong channel.
			if mem.limits.Addr64 {
				st.pushI64(mem.grow(uint64(st.popI64())))
			} else {
				st.pushI32(int32(mem.grow(uint64(uint32(st.popI32())))))
			}

		default:
			return unsupported(ins)
		}
	}
	// The body ran off its end without an END.
	//
	// The decoder cannot produce this — `endTerminator` is the only accepting exit from a
	// body read, so every retained body ends in 0x0b — which makes this the layering debt
	// again rather than a reachable path, and it says so instead of returning nil. A `nil`
	// here would let a hand-built or fuzz-mutated body fall out silently and be scored on
	// whatever the stack happened to hold.
	return fmt.Errorf("%w: function body ended without END", ErrNotValidated)
}

// unsupported names the opcode the engine has no arm for, in the table's own two-field
// shape.
//
// The bytes are in the message because the message *is* the work list: a fail bucket reading
// `interp: no arm for opcode fd 0f` names SIMD and one reading `interp: no arm for opcode 02`
// names control flow, which is what makes the board's biggest bucket answer the question of
// what to write next. Rendering matches `illegalPrefixed` — prefix and sub-opcode when there
// is a prefix, the byte alone when there is not — so the two layers' messages read alike for
// the same instruction.
func unsupported(ins binary.Instr) error {
	if ins.Prefix != 0x00 {
		return fmt.Errorf("%w %02x %02x", ErrUnsupportedOp, ins.Prefix, ins.Op)
	}
	return fmt.Errorf("%w %02x", ErrUnsupportedOp, ins.Op)
}

// badLocal reports a local index the frame does not have.
func badLocal(idx uint64, n int) error {
	return fmt.Errorf("%w: local.* index %d of %d locals", ErrNotValidated, idx, n)
}

// needNum reports whether the numeric stack holds at least n values.
//
// **Every arm calls it, and that is a deliberate cost** (#9's absence, priced). Stack
// underflow is a *validation* verdict — `type mismatch` — so with the validator in place
// this check is provably dead, and the honest thing is to say so at one place and let the
// arms be uniform rather than to reason per-arm about which pops are safe. What it must not
// do is return the spec string: reporting `type mismatch` from here would put #9's answer
// somewhere #9 cannot be tested from.
//
// The alternative was to let Go's slice bounds check do it and recover from the panic, which
// is worse in the way that matters: a `recover` converts an engine bug into an
// indistinguishable module verdict, so a genuine defect in an arm's arity would be reported
// as an unvalidated module and land in the same bucket.
func (s *stack) needNum(n int) error {
	if len(s.num) < n {
		return fmt.Errorf("%w: stack has %d values, an instruction wanted %d",
			ErrNotValidated, len(s.num), n)
	}
	return nil
}

// pushBool pushes a comparison's i32 result.
func (s *stack) pushBool(b bool) {
	if b {
		s.pushNum(1)
		return
	}
	s.pushNum(0)
}

// trapUnreachable is `unreachable`'s trap, whose spec text is the instruction's own name.
var trapUnreachable = &Trap{Reason: "unreachable"}

// The sign bits, named because `abs`, `neg` and `copysign` are defined on them rather than
// on a float value — see the f32 arithmetic arms.
const (
	signMask32 uint32 = 1 << 31
	signMask64 uint64 = 1 << 63
)

// The canonical NaNs: exponent all ones, payload's most significant bit set, everything
// below it clear.
//
// **Not Go's `math.NaN()`, and the difference is one bit that decides vectors.**
// `math.NaN()` is `0x7FF8000000000001` — the low payload bit is set — which is *an*
// arithmetic NaN but is not the canonical one, so every `nan:canonical` assertion would
// fail against it while every `nan:arithmetic` assertion passed. That is the shape of defect
// worth a named constant: a wrong value that is right most of the time.
const (
	canonicalNaN32 uint32 = 0x7FC0_0000
	canonicalNaN64 uint64 = 0x7FF8_0000_0000_0000
)

// canon32 and canon64 replace any NaN result with the canonical NaN.
//
// # Why canonicalizing is correct rather than merely convenient
//
// The spec's two rules are asymmetric. When **no operand is a NaN** and the result is a NaN
// — `0/0`, `inf - inf`, `sqrt(-1)` — the result *must* be the canonical NaN, and the suite
// asserts `nan:canonical`. When **an operand is a NaN**, the result may be any NaN, and the
// suite asserts `nan:arithmetic`, which the reference checks by testing that the payload's
// most significant bit is set.
//
// Canonical NaN satisfies both, because its payload MSB is set — so it *is* an arithmetic
// NaN. One rule therefore discharges both obligations, and it discharges them without
// depending on what the host CPU chose to do with an incoming payload. That last part is the
// real argument: propagating the hardware's NaN would pass the same vectors today on amd64
// and arm64 and be a portability bet, where this is a computation.
//
// It is applied to arithmetic only. `abs`, `neg`, `copysign` and the four reinterprets are
// payload-preserving by definition and are implemented as bit operations that never reach
// here; `f32.wast` asserts exact payloads such as `nan:0x200000` for exactly those.
func canon32(v float32) float32 {
	if v != v { // the only expression that is true for a NaN and nothing else
		return math.Float32frombits(canonicalNaN32)
	}
	return v
}

func canon64(v float64) float64 {
	if v != v {
		return math.Float64frombits(canonicalNaN64)
	}
	return v
}

// cmpI32 is the i32 relational block, 0x46..0x4f in the table's order.
//
// The signed and unsigned halves differ only in the cast, which is what a bare-uint64 slot
// buys: an i32 is 32 bits of a word and the *opcode* decides how to read them, so `lt_s` and
// `lt_u` are one comparison over two interpretations rather than two representations.
func cmpI32(op uint32, a, b int32) bool {
	switch op {
	case 0x46:
		return a == b
	case 0x47:
		return a != b
	case 0x48:
		return a < b
	case 0x49:
		return uint32(a) < uint32(b)
	case 0x4a:
		return a > b
	case 0x4b:
		return uint32(a) > uint32(b)
	case 0x4c:
		return a <= b
	case 0x4d:
		return uint32(a) <= uint32(b)
	case 0x4e:
		return a >= b
	}
	return uint32(a) >= uint32(b) // 0x4f i32.ge_u
}

// cmpI64 is the i64 relational block, 0x51..0x5a.
func cmpI64(op uint32, a, b int64) bool {
	switch op {
	case 0x51:
		return a == b
	case 0x52:
		return a != b
	case 0x53:
		return a < b
	case 0x54:
		return uint64(a) < uint64(b)
	case 0x55:
		return a > b
	case 0x56:
		return uint64(a) > uint64(b)
	case 0x57:
		return a <= b
	case 0x58:
		return uint64(a) <= uint64(b)
	case 0x59:
		return a >= b
	}
	return uint64(a) >= uint64(b) // 0x5a i64.ge_u
}

// cmpF32 is the f32 relational block, 0x5b..0x60. See the float-comparison arms for why
// Go's operators are the whole implementation.
func cmpF32(op uint32, a, b float32) bool {
	switch op {
	case 0x5b:
		return a == b
	case 0x5c:
		return a != b
	case 0x5d:
		return a < b
	case 0x5e:
		return a > b
	case 0x5f:
		return a <= b
	}
	return a >= b // 0x60 f32.ge
}

// cmpF64 is the f64 relational block, 0x61..0x66.
func cmpF64(op uint32, a, b float64) bool {
	switch op {
	case 0x61:
		return a == b
	case 0x62:
		return a != b
	case 0x63:
		return a < b
	case 0x64:
		return a > b
	case 0x65:
		return a <= b
	}
	return a >= b // 0x66 f64.ge
}

// binI32 is the i32 binary arithmetic block, 0x6a..0x78 — the only integer arm that traps.
//
// # The two traps and the one value that is both
//
// `div_s` traps twice and the second case is the interesting one: `INT_MIN / -1` has no
// representable quotient, so it is `integer overflow` rather than a wrapped result. `rem_s`
// on the same operands does **not** trap — the remainder is 0 and is perfectly
// representable — so the pair is a place where the obvious shared guard is wrong. Go happens
// to define `INT_MIN % -1` as 0 (the spec's two's-complement clause), but the case is
// written out rather than left to that: the arm's correctness should not depend on a reader
// knowing which of Go's two overflow clauses applies.
//
// # Shift counts are taken modulo the width, and that is the spec, not a convenience
//
// `i32.shl` by 32 is a shift by 0, not zero — `i32.wast` asserts it. Go's shift by a count
// ≥ the width yields 0 instead, so the mask is load-bearing rather than defensive. The count
// is read *unsigned* for the same reason: a negative i32 count is a large unsigned one, and
// `int(a) % 32` on a negative would go the wrong way.
func binI32(op uint32, a, b int32) (int32, error) {
	switch op {
	case 0x6a:
		return a + b, nil
	case 0x6b:
		return a - b, nil
	case 0x6c:
		return a * b, nil
	case 0x6d: // div_s
		if b == 0 {
			return 0, trapDivByZero
		}
		if a == math.MinInt32 && b == -1 {
			return 0, trapIntOverflow
		}
		return a / b, nil
	case 0x6e: // div_u
		if b == 0 {
			return 0, trapDivByZero
		}
		return int32(uint32(a) / uint32(b)), nil
	case 0x6f: // rem_s
		if b == 0 {
			return 0, trapDivByZero
		}
		if b == -1 {
			// Representable for every a, including MinInt32, and therefore not the
			// overflow div_s reports one arm up.
			return 0, nil
		}
		return a % b, nil
	case 0x70: // rem_u
		if b == 0 {
			return 0, trapDivByZero
		}
		return int32(uint32(a) % uint32(b)), nil
	case 0x71:
		return a & b, nil
	case 0x72:
		return a | b, nil
	case 0x73:
		return a ^ b, nil
	case 0x74:
		return int32(uint32(a) << (uint32(b) % 32)), nil
	case 0x75:
		return a >> (uint32(b) % 32), nil
	case 0x76:
		return int32(uint32(a) >> (uint32(b) % 32)), nil
	case 0x77:
		return int32(bits.RotateLeft32(uint32(a), int(uint32(b)%32))), nil
	}
	// 0x78 i32.rotr — a rotate right by k is a rotate left by -k, which is what
	// RotateLeft32's negative argument means.
	return int32(bits.RotateLeft32(uint32(a), -int(uint32(b)%32))), nil
}

// binI64 is the i64 binary arithmetic block, 0x7c..0x8a. Same shape as binI32, at 64 bits
// and modulo 64.
func binI64(op uint32, a, b int64) (int64, error) {
	switch op {
	case 0x7c:
		return a + b, nil
	case 0x7d:
		return a - b, nil
	case 0x7e:
		return a * b, nil
	case 0x7f: // div_s
		if b == 0 {
			return 0, trapDivByZero
		}
		if a == math.MinInt64 && b == -1 {
			return 0, trapIntOverflow
		}
		return a / b, nil
	case 0x80: // div_u
		if b == 0 {
			return 0, trapDivByZero
		}
		return int64(uint64(a) / uint64(b)), nil
	case 0x81: // rem_s
		if b == 0 {
			return 0, trapDivByZero
		}
		if b == -1 {
			return 0, nil
		}
		return a % b, nil
	case 0x82: // rem_u
		if b == 0 {
			return 0, trapDivByZero
		}
		return int64(uint64(a) % uint64(b)), nil
	case 0x83:
		return a & b, nil
	case 0x84:
		return a | b, nil
	case 0x85:
		return a ^ b, nil
	case 0x86:
		return int64(uint64(a) << (uint64(b) % 64)), nil
	case 0x87:
		return a >> (uint64(b) % 64), nil
	case 0x88:
		return int64(uint64(a) >> (uint64(b) % 64)), nil
	case 0x89:
		return int64(bits.RotateLeft64(uint64(a), int(uint64(b)%64))), nil
	}
	// 0x8a i64.rotr
	return int64(bits.RotateLeft64(uint64(a), -int(uint64(b)%64))), nil
}

// unF32 is the f32 unary arithmetic block that is *not* sign-bit work: 0x8d..0x91.
//
// `nearest` is round-half-to-**even**, which is `math.RoundToEven` and emphatically not
// `math.Round` — `f32.wast` asserts `nearest(0.5) == 0.0`, where `Round` gives 1.0. Computing
// it at float64 and narrowing is exact: the input is a float32 widened losslessly, and an
// integral result of magnitude within float32's range is representable in float32, so no
// double rounding is possible. `ceil`, `floor` and `trunc` are integral for the same reason.
//
// `sqrt` also computes at float64 and narrows, and that one needs the argument rather than
// the intuition: a correctly-rounded float64 sqrt narrowed to float32 equals the
// correctly-rounded float32 sqrt, because float64's 53 bits exceed the 2·24+2 needed to make
// the second rounding exact. Doing this with the wrong operation — `add`, say — would be
// double rounding and wrong, which is why the four arithmetic operations below stay in
// float32.
func unF32(op uint32, a float32) float32 {
	switch op {
	case 0x8d:
		return float32(math.Ceil(float64(a)))
	case 0x8e:
		return float32(math.Floor(float64(a)))
	case 0x8f:
		return float32(math.Trunc(float64(a)))
	case 0x90:
		return float32(math.RoundToEven(float64(a)))
	}
	return float32(math.Sqrt(float64(a))) // 0x91 f32.sqrt
}

// binF32 is the f32 binary arithmetic block, 0x92..0x97.
//
// **The four arithmetic operations stay in float32 and are not widened**, which is the
// mirror of unF32's argument: Go rounds a float32 expression to float32, and computing
// `a + b` at float64 and narrowing would round twice and disagree with IEEE 754 single
// precision on the ties. The unary arm can widen because its operations are exact there;
// this one cannot.
func binF32(op uint32, a, b float32) float32 {
	switch op {
	case 0x92:
		return a + b
	case 0x93:
		return a - b
	case 0x94:
		return a * b
	case 0x95:
		return a / b
	case 0x96:
		return fmin32(a, b)
	}
	return fmax32(a, b) // 0x97 f32.max
}

// unF64 is the f64 unary arithmetic block, 0x9b..0x9f.
func unF64(op uint32, a float64) float64 {
	switch op {
	case 0x9b:
		return math.Ceil(a)
	case 0x9c:
		return math.Floor(a)
	case 0x9d:
		return math.Trunc(a)
	case 0x9e:
		return math.RoundToEven(a)
	}
	return math.Sqrt(a) // 0x9f f64.sqrt
}

// binF64 is the f64 binary arithmetic block, 0xa0..0xa5.
func binF64(op uint32, a, b float64) float64 {
	switch op {
	case 0xa0:
		return a + b
	case 0xa1:
		return a - b
	case 0xa2:
		return a * b
	case 0xa3:
		return a / b
	case 0xa4:
		return fmin64(a, b)
	}
	return fmax64(a, b) // 0xa5 f64.max
}

// fmin32, fmax32, fmin64, fmax64 are wasm's min and max.
//
// **Written out rather than delegated to `math.Min`/`math.Max`, for one reason and it is not
// the zeros.** Go's `math.Min` gets the signed zeros right — `Min(-0, +0)` is `-0` — and gets
// NaN right in the sense that it returns *a* NaN. What it returns is `math.NaN()`, which is
// `0x7FF8000000000001` and therefore not canonical, so it would answer `nan:arithmetic`
// correctly and `nan:canonical` wrongly. Since these must feed `canon64` anyway, returning a
// bare NaN here and letting the caller canonicalize is both shorter and the thing that makes
// the canonical assertions pass.
//
// The zero case is still explicit rather than left to `<`: `-0.0 < +0.0` is **false** in IEEE
// 754, so a naive `if a < b { return a }` returns `+0` for `min(+0, -0)` depending on operand
// order — a wrong answer that is invisible on any vector using nonzero operands. The sign bit
// is the discriminator, which is why the test is on bits rather than on values.
func fmin32(a, b float32) float32 {
	switch {
	case a != a || b != b:
		return canonNaN32()
	case a == 0 && b == 0:
		// Both zero, possibly differing in sign: min is -0 if either is -0.
		return math.Float32frombits(math.Float32bits(a) | math.Float32bits(b))
	case a < b:
		return a
	}
	return b
}

func fmax32(a, b float32) float32 {
	switch {
	case a != a || b != b:
		return canonNaN32()
	case a == 0 && b == 0:
		// max is +0 unless both are -0, which is exactly the sign bits' AND.
		return math.Float32frombits(math.Float32bits(a) & math.Float32bits(b))
	case a > b:
		return a
	}
	return b
}

func fmin64(a, b float64) float64 {
	switch {
	case a != a || b != b:
		return canonNaN64()
	case a == 0 && b == 0:
		return math.Float64frombits(math.Float64bits(a) | math.Float64bits(b))
	case a < b:
		return a
	}
	return b
}

func fmax64(a, b float64) float64 {
	switch {
	case a != a || b != b:
		return canonNaN64()
	case a == 0 && b == 0:
		return math.Float64frombits(math.Float64bits(a) & math.Float64bits(b))
	case a > b:
		return a
	}
	return b
}

func canonNaN32() float32 { return math.Float32frombits(canonicalNaN32) }
func canonNaN64() float64 { return math.Float64frombits(canonicalNaN64) }

// truncToI32 is the four float-to-i32 truncations, 0xa8..0xab.
//
// # Two traps, and which one applies is decided before the truncation
//
// A NaN input is `invalid conversion to integer`; a finite-but-out-of-range input is
// `integer overflow`. Both are asserted by `conversions.wast`, and getting the order wrong
// swaps the messages on `trunc_f32_s(nan)`.
//
// # The bounds are stated as exact powers of two, and the comparison direction differs per side
//
// The signed range is `-2^31 ≤ trunc(x) < 2^31`, so the upper test is `>=` against 2^31 and
// the lower is `<` against -2^31: both bounds are exactly representable in float64, and 2^31
// itself is *out* of range while -2^31 is *in* it. Writing the upper bound as `> 2147483647`
// would be wrong for any x in (2^31 - 1, 2^31), which float32 inputs can reach. The unsigned
// range is `-1 < x < 2^32`, which after truncation is `0 ≤ trunc(x) < 2^32` — and the lower
// test is on the truncated value, so `trunc(-0.9) == -0.0` passes it and yields 0, as the
// suite requires.
//
// Truncating at float64 is exact for both input widths: a float32 widens losslessly, and
// `math.Trunc` is exact.
func truncToI32(op uint32, st *stack) (int32, error) {
	var d float64
	if op == 0xa8 || op == 0xa9 {
		d = float64(st.popF32())
	} else {
		d = st.popF64()
	}
	if d != d {
		return 0, trapBadConvert
	}
	d = math.Trunc(d)
	signed := op == 0xa8 || op == 0xaa
	if signed {
		if d >= 1<<31 || d < -(1<<31) {
			return 0, trapIntOverflow
		}
		return int32(d), nil
	}
	if d >= 1<<32 || d < 0 {
		return 0, trapIntOverflow
	}
	return int32(uint32(d)), nil
}

// truncToI64 is the four float-to-i64 truncations, 0xae..0xb1. Same shape as truncToI32 at
// 64 bits; 2^63 and 2^64 are likewise exactly representable in float64.
func truncToI64(op uint32, st *stack) (int64, error) {
	var d float64
	if op == 0xae || op == 0xaf {
		d = float64(st.popF32())
	} else {
		d = st.popF64()
	}
	if d != d {
		return 0, trapBadConvert
	}
	d = math.Trunc(d)
	signed := op == 0xae || op == 0xb0
	if signed {
		if d >= 1<<63 || d < -(1<<63) {
			return 0, trapIntOverflow
		}
		return int64(d), nil
	}
	if d >= 1<<64 || d < 0 {
		return 0, trapIntOverflow
	}
	return int64(uint64(d)), nil
}
