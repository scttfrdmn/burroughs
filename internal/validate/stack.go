// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"fmt"
	"strings"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// validator is the abstract machine valid.ml describes: one operand-type stack and one
// control-frame stack, walked in lockstep with the instruction sequence.
type validator struct {
	mod    *binary.Module
	locals []binary.ValType

	// curFunc is the body being checked, for the side tables 0016 puts beside it — br_table's
	// label vector is the one slice 1 reads.
	curFunc *binary.Func

	// refs is `context.refs` restricted to the function index space: the set `ref.func` may name
	// (`valid.ml:1152`, and declaredFuncs for what contributes to it). Module-scoped and computed
	// once, like the reference's, because it is a property of the module and not of the body — a
	// body's own references are excluded from it by construction.
	refs map[uint32]bool

	// stack holds operand *types*. Its height is not the slot count — see slots().
	stack  []binary.ValType
	frames []frame

	blocks   map[int]Arity
	maxStack int
}

// frame is one entry on the control stack.
type frame struct {
	// kind is the opener, so `br` can tell a loop's label (parameters) from a block's
	// (results) without re-reading the instruction.
	kind uint32

	labelTypes []binary.ValType // what a `br` to this label carries
	endTypes   []binary.ValType // what the frame leaves behind when it completes

	// params is the block type's parameter list, retained because `else` has to restore it: the
	// else-arm starts from the same stack the then-arm did.
	params []binary.ValType

	// height is the operand-stack height on entry. Everything above it belongs to this frame,
	// which is what makes `unreachable` able to discard exactly this frame's operands and no
	// more.
	height int

	// unreachable is the polymorphic state: after `unreachable`, `br`, `return`, or an
	// unconditional trap, the rest of the frame type-checks against a stack that supplies any
	// type on demand. This is valid.ml's bottom, and it is the single most load-bearing bit in
	// the algorithm — without it `(unreachable) (i32.add)` is a spurious mismatch, and
	// `unreached-invalid.wast`'s 121 vectors exist to test exactly this axis.
	unreachable bool

	// elseSeen guards `if`'s two-arm shape: `else` may appear once, and only inside an `if`.
	elseSeen bool
}

// opFuncBody is a pseudo-opcode for the function-body frame, which no instruction opens.
//
// A distinct value rather than reusing `block`'s 0x02: the body frame is the one frame `else`
// must never match and `end` must not pop early, and giving it a real opcode would make both
// checks say "block" in their testimony about a frame that is not one.
const opFuncBody = 0xFFFF_FFFF

func (v *validator) pushFrame(kind uint32, labelTypes, endTypes []binary.ValType) {
	v.frames = append(v.frames, frame{
		kind:       kind,
		labelTypes: labelTypes,
		endTypes:   endTypes,
		height:     len(v.stack),
	})
}

func (v *validator) top() *frame { return &v.frames[len(v.frames)-1] }

// push records one operand type.
func (v *validator) push(t binary.ValType) {
	v.stack = append(v.stack, t)
	if n := v.slots(); n > v.maxStack {
		v.maxStack = n
	}
}

// slots is the stack's depth in machine slots rather than in values, because 0024 makes a v128
// occupy two adjacent 64-bit slots everywhere a slot is a thing.
//
// Counted here rather than in the interpreter for the reason FuncInfo exists at all: the
// validator already knows every operand's type, and a second pass would be a second derivation
// of one fact — the shape that produced the `Casts`/`Labels` side tables' own bugs.
func (v *validator) slots() int {
	n := 0
	for _, t := range v.stack {
		if t == binary.V128 {
			n += 2
			continue
		}
		n++
	}
	return n
}

// pop removes one operand and returns its type.
//
// In the unreachable state a frame's stack is bottomless: popping below the frame's entry height
// yields `unknown`, which matches everything. Above the height it pops for real, so
// `(unreachable) (i32.const 1) (i32.add)` still checks the operand it can see.
func (v *validator) pop() (binary.ValType, bool) {
	f := v.top()
	if len(v.stack) == f.height {
		if f.unreachable {
			return unknown, true
		}
		return binary.ValType{}, false
	}
	t := v.stack[len(v.stack)-1]
	v.stack = v.stack[:len(v.stack)-1]
	return t, true
}

// unknown is valid.ml's bottom *valtype* — `BotT` — the operand an unreachable frame supplies on
// demand.
//
// Spelled as an indexed reftype with an index no module can hold rather than as a new field on
// binary.ValType, because bottom is this package's concept and not the wire format's. The wire
// has no encoding for it; putting it in `binary` would be inventing a type the format does not
// have, and every `switch` over kinds elsewhere would gain a case for something no image can
// contain.
var unknown = binary.RefType(botHeapIdx, true)

// botHeapIdx is `BotHT`, the bottom *heaptype*, as an index sentinel — and the reason it is named
// apart from `unknown` is that the reference has two bottoms and slice 8 is where the difference
// became a verdict.
//
// `BotT` (this file's `unknown`) is `match_valtype`'s `BotT, _ -> true`: it satisfies a numeric
// requirement. `RefT (nul, BotHT)` is `match_heaptype`'s `BotHT, _ -> true` reached *through*
// `match_reftype`, so it satisfies every reference requirement and **no numeric one** — the
// mixed-sort pair falls to `match_valtype`'s `_, _ -> false`.
//
// `unreached-invalid.wast:697` — `(unreachable) (ref.as_non_null) (f32.abs)` — is the single row in
// the whole 55-row slice-8 population that can tell the two readings apart, and **which mutation it
// tells apart was measured rather than reasoned**, because the first draft of this sentence named
// the wrong one. Conflating the two bottoms *where peekRef answers* fails nothing: the arms
// overwrite the null bit on both the pop and the push, so `unknown` and `botRef(false)` are
// indistinguishable there. The row keys on the **push** — a bottom operand that comes back out as
// the valtype bottom rather than as a reference bottom is what makes `f32.abs` typecheck against
// it. See botRef, peekRef's third section, and ADR 0034's falsification bill.
//
// One index for both, with the null bit carrying the difference in representation: `unknown` is
// `botRef(true)` by construction below, so the valtype bottom and the *nullable* reference bottom
// are the same value. That coincidence is sound and is not an accident to be tolerated silently —
// the nullable reference bottom only ever appears as a **want** (`br_on_null` and
// `ref.as_non_null` pop `RefT (Null, ht)` and push `RefT (NoNull, ht)`), and a want is reached only
// after `peekRef` has already found bottom on the stack, which means the got side is bottom too.
// So no comparison can distinguish them. The **non-nullable** reference bottom is the one that gets
// pushed, and that one is a distinct value from `unknown`.
const botHeapIdx = ^uint32(0)

// botRef is `peek_ref`'s bottom answer — `RefT (nul, BotHT)` (`valid.ml:288`, `| BotT -> (NoNull,
// BotHT)`).
//
// A function rather than two vars so that a caller spells the null bit it means, which is the whole
// content of the rule it appears in: `ref.as_non_null` pops the nullable one and pushes the
// non-nullable one, and those are the two halves of what the instruction does.
func botRef(null bool) binary.ValType { return binary.RefType(botHeapIdx, null) }

// isBotHeap reports whether t is a reference whose heaptype is bottom — either bottom, since
// `match_heaptype` never reads the null bit (matchRefType's division).
func isBotHeap(t binary.ValType) bool { return t.IsIndexed() && t.Index() == botHeapIdx }

// matches reports whether an operand of type `got` satisfies a requirement of type `want`.
//
// **This was identity plus a bottom wildcard, under a comment declaring that limit slice 1's**,
// and decision 0031 retired the declaration. The prior text, quoted because a retired boundary
// is recorded rather than absorbed:
//
//	Bottom matches anything in either direction. **Everything else is identity, and that is
//	slice 1's declared limit rather than an oversight**: proper subtyping — `(ref $t) <: (ref
//	null $t)`, `eq <: any`, the whole `match.ml` relation — is the GC slice's, and its vectors
//	expect `sub type` rather than `type mismatch`. Identity is exactly right for the numeric
//	families, which is what this slice type-checks; a reference-typed operand reaches here only
//	through local/global/select, and a wrong answer there is a *reject* of a valid module, which
//	the suite's accept direction does see.
//
// Its last clause was **false when it was written and true only later**: the accept direction had
// no witness at all until #341 built one, so at the time the comment claimed the suite would see
// a wrong reject, nothing scored module definitions on the validator's answer. The nine
// over-rejections #343 tracked were invisible for exactly as long as that sentence stood.
//
// What it delegates to is `match.go`, which is `match_valtype` (match.ml:110-116). The module is
// needed because the relation is not local to the two types: an indexed reference form's place in
// the lattice comes from the definition it names.
//
// Both sides of the context are the same module here, and that is the validator's whole use of
// the relation: it compares two types drawn from one type section, so `tctx.same` holds and the
// index-equality shortcuts apply. The linker is the caller for which they do not (#368).
func (v *validator) matches(got, want binary.ValType) bool {
	return matchValType(tctx{gotMod: v.mod, wantMod: v.mod}, got, want)
}

// popExpect pops one operand and requires it to satisfy want.
func (v *validator) popExpect(want binary.ValType) error {
	got, ok := v.pop()
	if !ok {
		return fmt.Errorf("%w: expected %s, stack empty", ErrTypeMismatch, typeStr(want))
	}
	if !v.matches(got, want) {
		return fmt.Errorf("%w: expected %s, got %s", ErrTypeMismatch, typeStr(want), typeStr(got))
	}
	return nil
}

// popExpectAll pops a sequence of operands, rightmost first.
//
// The reversal is the whole point and is easy to get backwards: `types` reads in stack order
// (first pushed first), so they must be popped from the end. A left-to-right version passes
// every homogeneous signature — `(i32, i32) -> i32` never notices — and fails only on the mixed
// ones, which is protection by coincidence for the majority of the corpus.
func (v *validator) popExpectAll(types []binary.ValType) error {
	for i := len(types) - 1; i >= 0; i-- {
		if err := v.popExpect(types[i]); err != nil {
			return err
		}
	}
	return nil
}

// peekN reads the top n operand types without popping them, bottom-padded.
//
// The pad is what makes it different from n pops: a slot below the frame's entry height reads as
// `unknown` **whether or not the frame is unreachable**, because peeking is not a claim that the
// value is there. That claim is popExpectAll's, and separating them is the reference's own division
// (`peek` returns BotT out of range unconditionally; `pop` pads only for a polymorphic stack and
// fails on length otherwise). br_table is the one caller: it needs the operand types in order to
// check its arms *before* it is entitled to consume them.
func (v *validator) peekN(n int) []binary.ValType {
	f := v.top()
	ts := make([]binary.ValType, n)
	for j := range n {
		if k := len(v.stack) - n + j; k >= f.height {
			ts[j] = v.stack[k]
			continue
		}
		ts[j] = unknown
	}
	return ts
}

// typeList renders a type sequence for an error message. `[]` for the empty one, because an error
// reading "takes , stack has i32" hides its own most informative case.
func typeList(ts []binary.ValType) string {
	if len(ts) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, t := range ts {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(typeStr(t))
	}
	b.WriteByte(']')
	return b.String()
}

// typeStr renders one type for an error message, which for the bottoms is the only place they can
// be rendered honestly.
//
// **`binary.ValType.String()` cannot do this and must not be asked to.** The two bottoms are
// spelled as indexed reference types naming an index no module can hold — this package's own
// sentinel choice, per `unknown`'s comment — so `String()` prints `(ref 4294967295)`, an index the
// module does not have and never had. A message asserting that is grave #36's class: the engine
// inventing a fact about its input. `binary` is the wrong place for the fix because bottom is not a
// wire form, so the renderer lives beside the sentinels that need it.
//
// The two are printed apart. `bot` is the valtype bottom an unreachable frame supplies; `(ref bot)`
// is `peek_ref`'s reference bottom, and telling a reader which one a message is about is the
// difference slice 8 exists to make representable.
func typeStr(t binary.ValType) string {
	switch {
	case t == unknown:
		return "bot"
	case isBotHeap(t):
		return "(ref bot)"
	}
	return t.String()
}

// pushAll pushes a sequence in stack order.
func (v *validator) pushAll(types []binary.ValType) {
	for _, t := range types {
		v.push(t)
	}
}

// expectEmptyFrame requires the current frame to have consumed everything it produced.
//
// # The unreachable state does not exempt leftovers, and this asked the authority to be sure
//
// This function discarded them instead, under a comment claiming the asymmetry was valid.ml's and
// that enforcing it "rejects valid modules after a `br`". Neither half was true, and the corpus
// said so: 19 of `unreached-invalid.wast`'s vectors — `(func (unreachable) (i32.const 0))`,
// `(func (block (br 0) (i32.const 1)))`, every `*-after-break`/`*-after-return`/`*-after-
// unreachable` row — were *accepted* by exactly that discard.
//
// The reference's end-of-block check is three lines of `check_block` (`valid/valid.ml:966-972`):
//
//	let s, xs' = check_instrs c (stack ts1) es in
//	let s' = pop c (stack ts2) s at in
//	require (snd s' = []) at …
//
// `s` carries the polymorphic marker in its *first* component (the `Ellipses` flag), and the
// requirement is on `snd s'` — the concrete residue after the block's results are popped. So the
// two roles are separate and only one of them is polymorphic: **an unreachable stack supplies
// values it does not have** (`pop` pads with `BotT`, which is this package's `unknown`) **and
// still may not keep values it was not asked for.** A value pushed after `unreachable` was pushed
// by a real instruction and has to go somewhere.
//
// Nothing is discarded here, which also means this no longer mutates the stack — the truncation
// belongs to setUnreachable, where it is the operand-clearing half of entering the state.
func (v *validator) expectEmptyFrame() error {
	f := v.top()
	if n := len(v.stack) - f.height; n != 0 {
		return fmt.Errorf("%w: %d value(s) left on the stack at the end of a block",
			ErrTypeMismatch, n)
	}
	return nil
}

// setUnreachable enters the polymorphic state and discards the frame's operands.
func (v *validator) setUnreachable() {
	f := v.top()
	v.stack = v.stack[:f.height]
	f.unreachable = true
}

// label resolves a relative label index to its frame.
func (v *validator) label(depth uint32) (*frame, error) {
	if depth >= uint32(len(v.frames)) {
		return nil, fmt.Errorf("%w %d (%d frames in scope)", ErrUnknownLabel, depth, len(v.frames))
	}
	return &v.frames[len(v.frames)-1-int(depth)], nil
}
