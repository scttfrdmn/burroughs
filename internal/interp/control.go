package interp

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The structured control flow arms: block, loop, if/else, br, br_if, br_table, return (#7).
//
// # Target resolution: what this does, and the debt it takes
//
// A branch names a *label* — a count of enclosing blocks to exit — and the engine has to turn
// that into an index in `[]Instr`. Three places can do it:
//
//   - **At branch time**, scanning for the matching delimiter on every branch.
//   - **At block entry**, scanning once when the block is pushed, so each branch out of it is
//     an assignment to `pc`. **This is what the code below does** (`matchEnd`, `elseOf`).
//   - **At build time**, one pass per body recording every pairing, so entering a block is also
//     free. This is 0002's letter — "decode once into `[]ins` with pre-decoded immediates and
//     resolved targets".
//
// **So this is not yet 0002's form, and the gap is stated rather than glossed.** Entry-time
// resolution is correct and needs no new state; what it costs is a scan per *dynamic* block
// entry, which in a loop body is paid per iteration. That is precisely the shape of cost 0002
// rejected the side table over — build cost amortizes on §1's workload, hot-loop cost does not —
// so the third option is the one this engine should end at, and it is deferred here rather than
// declined: it wants the pairing to live beside the body, which is a retention question in
// `binary` (a parallel array keyed by instruction index) that `br_table`'s label vector also
// needs and that neither has yet. Filed as **#136**, whose definition of done is a *benchmark*
// rather than a vector — both readings are correct, so the suite cannot see the difference and
// only a measurement can. The debt is a failing test rather than an intention.
//
// # Why the pairing is exact rather than approximate
//
// It relies on the decoder retaining both delimiters, which it does deliberately: `structural`
// emits the header *before* the nested body precisely so a target is an index into the same
// slice, and it emits `ELSE` because otherwise an `if`'s two arms are one undifferentiated run
// with no declared lengths. So the pairing is a paren-match over a sequence where every opener
// and every closer is present as an instruction — no re-decoding, no arithmetic on lengths.
//
// # What is absent, and named so the next author does not read it as done
//
// `br_table` (0x0e) **is** here now — its arm is in exec.go and its label vector is retained in
// `Func.Labels`, the side table keyed by instruction index that 0016 records. The paragraph this
// replaces said it was absent and deferred the retention, which was true for one PR; keeping the
// sentence would have made this file assert the opposite of what the engine does. What the
// retention did *not* do is settle #136: the side table holds the labels as written, and the
// block-pairing question above is still resolved at block entry.
//
// Still absent: `call`/`call_indirect` (0x10/0x11). The first needs a frame stack, the second
// needs tables and element segments that `binary.Module` does not retain at all — and that
// retention is shaped by the wire form rather than by `call_indirect`'s convenience, because
// `table.init` and active-elem instantiation are its later consumers.

// label is one entry on the control stack: an active block, loop, or if.
type label struct {
	// cont is where a branch to this label lands, and **it differs by construct**, which is
	// the whole content of structured control flow. Branching to a `block` or an `if` exits
	// it, so cont is one past its END; branching to a `loop` re-enters it, so cont is the
	// instruction after the loop header. Getting this backwards makes every loop a straight
	// line and every block an infinite one, and both still terminate on many vectors — which
	// is why it is stated here rather than inferred at the branch site.
	cont int

	// arity is how many values the label yields — the count a branch must leave on the stack.
	// For a block or if that is the blocktype's result count; **for a loop it is the
	// parameter count**, because re-entering a loop supplies its parameters rather than its
	// results (`eval.ml`'s `Label (n, es)` is built from `blocktype`'s *input* arity for
	// Loop). One field with two derivations, not two fields, so a branch does not have to
	// know which construct it is leaving.
	arity int

	// height is the value stack depth at the point the label was pushed, excluding the
	// operands the block itself consumes. A branch truncates to height+arity: the spec's
	// "pop the values, pop the frames, push the values back".
	height int
}

// blockArity resolves a structural instruction's blocktype to its parameter and result counts.
//
// **Reads the blocktype through `binary.BlockType` rather than by unpacking Imm0 here**, so the
// packing rule lives only in the package that writes it.
//
// The type-index case is the one that can fail, and it fails as the layering debt: a blocktype
// naming a type the module does not declare, or naming a struct or array slot, is #9's verdict.
// The all-gates-on lane makes the second reachable, since `Module.Types` keeps GC slots so type
// indices do not shift.
func (in *Instance) blockArity(imm0 uint64) (params, results int, err error) {
	idx, vt, empty := binary.BlockType(imm0)
	switch {
	case empty:
		return 0, 0, nil
	case vt != 0:
		// A single result, no parameters — the `valtype` form cannot express either
		// parameters or a second result.
		return 0, 1, nil
	default:
		if int(idx) >= len(in.mod.Types) {
			return 0, 0, fmt.Errorf("%w: blocktype names type %d of %d",
				ErrNotValidated, idx, len(in.mod.Types))
		}
		ct := &in.mod.Types[idx]
		if ct.Kind != binary.CompFunc {
			return 0, 0, fmt.Errorf("%w: blocktype names type %d, which is a %s",
				ErrNotValidated, idx, ct.Kind)
		}
		return len(ct.Func.Params), len(ct.Func.Results), nil
	}
}

// matchEnd pairs the structural header at `pc` with its own END, returning that index.
//
// A paren match over the retained delimiters: every structural opcode opens, every `0x0b`
// closes, and `ELSE` closes nothing. Written as a scan rather than as a precomputed table
// because a table is per-body state and this is called once per *executed* block entry —
// see elseOf for the same shape and the measurement note in the file header.
//
// **The not-found case is the layering debt, not a panic.** The decoder cannot produce an
// unterminated body (`endTerminator` is the only accepting exit from a body read), so this
// returns an error rather than running off the end: a hand-built or fuzz-mutated body must
// not index past the slice.
func matchEnd(body []binary.Instr, pc int) (int, error) {
	depth := 0
	for i := pc; i < len(body); i++ {
		ins := body[i]
		if ins.Prefix != 0x00 {
			continue
		}
		switch ins.Op {
		case opBlock, opLoop, opIf, opTryTable:
			depth++
		case opEnd:
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, fmt.Errorf("%w: structural instruction at %d has no END", ErrNotValidated, pc)
}

// elseOf finds the ELSE belonging to the `if` at `pc`, or reports that there is none.
//
// Only an ELSE at **depth 1** relative to the header is this `if`'s: an ELSE nested inside the
// then-arm belongs to an inner `if`, and matching it would execute the inner else-arm as this
// one's. That is the defect this function exists to not have, and it is invisible on a board
// where most `if`s are shallow.
func elseOf(body []binary.Instr, pc, end int) (int, bool) {
	depth := 0
	for i := pc; i < end; i++ {
		ins := body[i]
		if ins.Prefix != 0x00 {
			continue
		}
		switch ins.Op {
		case opBlock, opLoop, opIf, opTryTable:
			depth++
		case opEnd:
			depth--
		case opElse:
			if depth == 1 {
				return i, true
			}
		}
	}
	return 0, false
}

// The structural and branch opcodes, named because a bare 0x02 in a switch arm is a byte and
// these are a family. Spelled here rather than in exec.go's constant blocks because every
// consumer of them is in this file.
const (
	opBlock    = 0x02
	opLoop     = 0x03
	opIf       = 0x04
	opElse     = 0x05
	opEnd      = 0x0b
	opBr       = 0x0c
	opBrIf     = 0x0d
	opBrTable  = 0x0e
	opReturn   = 0x0f
	opTryTable = 0x1f

	// The two `select` encodings. Not control flow, and here for the reason this block exists at
	// all: they are a family whose members differ only in an immediate the decoder discards, so a
	// bare `0x1b, 0x1c` in exec.go's switch would read as two unrelated bytes. `internal/text` has
	// its own pair under the same names, checked against the generated table and against the
	// decoder there; this package's copy is checked by TestSelectOpcodesAgreeWithTheDecoder.
	opSelect  = 0x1b
	opSelectT = 0x1c
)

// branch performs a branch to the label `depth` levels out, returning the pc to resume at.
//
// **The stack surgery is the spec's, in the spec's order**: keep the label's arity values from
// the top, drop everything the block pushed below them, then resume at the label's
// continuation. `eval.ml`'s Label admits `n` values and discards the rest, which for a `block`
// means its results survive and its scratch does not.
//
// Returns the *label index* it branched to as well, because `br 1` inside a block must unwind
// the control stack too — the caller pops back to that level.
func (in *Instance) branch(st *stack, ctrl []label, depth uint64) (pc, level int, err error) {
	if depth >= uint64(len(ctrl)) {
		// #9's `unknown label`, arriving as the layering debt: a branch past the outermost
		// enclosing block is a module the validator rejects. Reported here rather than as
		// the spec string, for needNum's reason — this package must not answer #9's
		// questions under #9's names.
		//
		// **`len(ctrl)+1` labels are in scope, not `len(ctrl)`**, and the message says so
		// because the extra one is legal: the implicit function-body label has no entry in
		// `ctrl` and is reached at depth `len(ctrl)` exactly, which the callers answer before
		// this function is asked (see opBr). So the first depth that arrives here is
		// `len(ctrl)+1`, and a message quoting `len(ctrl)` names a depth that *works* as the
		// bound — reporting `br 2` as out of range "with 2 labels in scope" when `br 2` is
		// precisely the return. The count is what a reader edits against, so it counts what
		// the engine will accept.
		return 0, 0, fmt.Errorf("%w: branch to label %d with %d labels in scope "+
			"(%d explicit, plus the implicit function body)",
			ErrNotValidated, depth, len(ctrl)+1, len(ctrl))
	}
	l := ctrl[len(ctrl)-1-int(depth)]
	// Keep the top `arity` values, truncate to the label's height, push them back. Done with
	// a copy inside the same slice rather than a temporary: the source range is above the
	// destination whenever anything is dropped, so a forward copy cannot overwrite an unread
	// element.
	if l.arity > 0 {
		src := len(st.num) - l.arity
		if src < l.height {
			// Fewer values on the stack than the label promises to yield. #9's arity
			// check again; without it the copy below would read below the block's base.
			return 0, 0, fmt.Errorf("%w: branch to a label of arity %d with %d values above its base",
				ErrNotValidated, l.arity, len(st.num)-l.height)
		}
		copy(st.num[l.height:], st.num[src:])
	}
	st.num = st.num[:l.height+l.arity]
	return l.cont, len(ctrl) - 1 - int(depth), nil
}

// returnFrom is the stack surgery a return performs: keep the function's results, drop
// everything else the body left behind.
//
// **`eval.ml:1069` is `take n vs0`, and `n` is the frame's arity** — a return truncates, exactly
// as a branch to a label does, because a function body *is* an implicit labelled block and the
// implicit label's arity is the function's result count. The arm this replaces returned without
// touching the stack, on the stated ground that "the values below the results belong to no one
// once this function is done". That is true of the *frame*, and it is not true of `Invoke`'s
// arity check, which counts what is left: `(i32.const 1) (return (i32.const 2))` in a
// `(result i32)` function leaves two values, and the check then rejects a valid module as
// unvalidated (grave #135). The defect was stated as the rule in a comment, which is why no
// review of that arm against its claim could find it.
//
// Reported rather than silently clamped when there are too few values: that is #9's arity
// question arriving late, the same reading `branch` gives it.
func returnFrom(st *stack, results int) error {
	if len(st.num) < results {
		return fmt.Errorf("%w: return with %d values on the stack, but the function declares %d results",
			ErrNotValidated, len(st.num), results)
	}
	copy(st.num, st.num[len(st.num)-results:])
	st.num = st.num[:results]
	return nil
}
