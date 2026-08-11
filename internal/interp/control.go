package interp

import (
	"errors"
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

	// arity is how many *numeric* values the label yields — the count a branch must leave on
	// the numeric stack. For a block or if that is the blocktype's numeric result count;
	// **for a loop it is the numeric parameter count**, because re-entering a loop supplies
	// its parameters rather than its results (`eval.ml`'s `Label (n, es)` is built from
	// `blocktype`'s *input* arity for Loop). One field with two derivations, not two fields,
	// so a branch does not have to know which construct it is leaving.
	arity int

	// refArity is arity's reference-stack twin (#196/#197) — the count of ref-typed values a
	// branch must leave on `st.refs`, same block-vs-loop derivation as arity. **Two fields,
	// not one shared count**, for the reason `call.go`'s `invoke` already established for a
	// callee's result arity: the two arrays have independent depths (0002), so one counter
	// cannot answer "how many of each" and a branch that only tracked arity would either
	// leave stale reference scratch behind or truncate a numeric-only block's (harmless, since
	// refArity is 0) reference stack incorrectly the day a block mixes kinds.
	refArity int

	// height is the *numeric* value stack depth at the point the label was pushed, excluding
	// the operands the block itself consumes. A branch truncates to height+arity: the spec's
	// "pop the values, pop the frames, push the values back".
	height int

	// refHeight is height's reference-stack twin, excluding the ref-typed operands the block
	// itself consumes.
	refHeight int

	// isHandler is true exactly for a label `try_table` pushed — every other construct leaves
	// it false. The scan that looks for an enclosing handler (tryCatch, exec.go) tests this
	// rather than `len(catches) > 0`, because a zero-clause `try_table` is legal (`decode.ml`'s
	// `vec (at catch) s` accepts an empty vector) and is still a handler — one that never
	// matches, so every exception thrown inside it falls through uncaught. `len(catches)==0`
	// cannot tell that case apart from "not a try_table at all" the way an explicit bool can.
	isHandler bool

	// catches is this label's try_table handler clauses, meaningful only when isHandler is
	// true. Empty for a zero-clause try_table, per isHandler's own comment.
	catches []binary.Catch
}

// blockArity resolves a structural instruction's blocktype to its parameter and result counts,
// each split into numeric and reference — #196/#197's widening of the pre-existing
// numeric-only signature, mirroring the split `call.go`'s `invoke` already makes for a callee's
// result arity (`wantNum`/`wantRef` there).
//
// **Reads the blocktype through `binary.BlockType` rather than by unpacking Imm0 here**, so the
// packing rule lives only in the package that writes it.
//
// The type-index case is the one that can fail, and it fails as the layering debt: a blocktype
// naming a type the module does not declare, or naming a struct or array slot, is #9's verdict.
// The all-gates-on lane makes the second reachable, since `Module.Types` keeps GC slots so type
// indices do not shift.
func (in *Instance) blockArity(imm0, imm1 uint64) (params, refParams, results, refResults int, err error) {
	idx, vt, empty := binary.BlockType(imm0, imm1)
	switch {
	case empty:
		return 0, 0, 0, 0, nil
	case vt != binary.NoValType:
		// A single result, no parameters — the `valtype` form cannot express either
		// parameters or a second result. `vt != NoValType` rather than a boolean third
		// return, preserving the pre-0018 predicate exactly: every valtype the decoder can
		// resolve has a non-zero kind (0x6E-0x80), so this is never true for the empty or
		// type-index cases, both of which BlockType returns as the zero ValType.
		if vt.IsRef() {
			return 0, 0, 0, 1, nil
		}
		return 0, 0, 1, 0, nil
	default:
		if int(idx) >= len(in.mod.Types) {
			return 0, 0, 0, 0, fmt.Errorf("%w: blocktype names type %d of %d",
				ErrNotValidated, idx, len(in.mod.Types))
		}
		ct := &in.mod.Types[idx]
		if ct.Kind != binary.CompFunc {
			return 0, 0, 0, 0, fmt.Errorf("%w: blocktype names type %d, which is a %s",
				ErrNotValidated, idx, ct.Kind)
		}
		np, nr := countByArray(ct.Func.Params)
		rp, rr := countByArray(ct.Func.Results)
		return np, nr, rp, rr, nil
	}
}

// countByArray partitions a functype's value-type slice into its numeric and reference counts
// — the same split `call.go`'s `invoke` inlines for `ft.Results` (`wantNum`/`wantRef`), named
// here because `blockArity` needs it twice (params and results) and `br_table`'s block-typed
// vectors are exactly what makes a block's *parameter* half need the split too, not only a
// callee's result half.
func countByArray(ts []binary.ValType) (numCount, refCount int) {
	for _, t := range ts {
		if t.IsRef() {
			refCount++
		} else {
			numCount++
		}
	}
	return numCount, refCount
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
	opThrow    = 0x08
	opThrowRef = 0x0a
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
	//
	// **Both arrays, independently, exactly as `call.go`'s `invoke` checks a callee's result
	// arity per array (#196/#197).** A block whose result type mixes a numeric and a
	// reference value (`br_table.wast`'s `meet-funcref-*`, `(result (ref null func))` blocks
	// nested with `(result (ref null $t))` ones) needs its ref-typed scratch discarded and
	// its ref-typed results kept exactly as its numeric ones are — a single shared arity
	// could not express "keep 1 numeric and 1 reference" any more than a single shared
	// result count could at the callee boundary.
	if l.arity > 0 {
		src := len(st.num) - l.arity
		if src < l.height {
			// Fewer values on the stack than the label promises to yield. #9's arity
			// check again; without it the copy below would read below the block's base.
			return 0, 0, fmt.Errorf("%w: branch to a label of arity %d with %d values above its base",
				ErrNotValidated, l.arity, len(st.num)-l.height)
		}
		copy(st.num[l.height:], st.num[src:])
		if st.tracking {
			// **0023's own truncation, carried along by the identical copy+reslice this
			// site already performs on `st.num`** — a sequence number belongs to its slot,
			// so whatever survives the branch keeps the number it arrived with, exactly as
			// its value does. No new algorithm; the existing per-array truncation pattern
			// extended one field.
			copy(st.numSeq[l.height:], st.numSeq[src:])
		}
	}
	st.num = st.num[:l.height+l.arity]
	if st.tracking {
		st.numSeq = st.numSeq[:l.height+l.arity]
	}
	if l.refArity > 0 {
		src := len(st.refs) - l.refArity
		if src < l.refHeight {
			return 0, 0, fmt.Errorf("%w: branch to a label of reference arity %d with %d references above its base",
				ErrNotValidated, l.refArity, len(st.refs)-l.refHeight)
		}
		copy(st.refs[l.refHeight:], st.refs[src:])
		copy(st.refSeq[l.refHeight:], st.refSeq[src:])
	}
	st.refs = st.refs[:l.refHeight+l.refArity]
	st.refSeq = st.refSeq[:l.refHeight+l.refArity]
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
// **Two arities, not one, since #196/#197** — mirroring `branch`'s own widening just above and
// `call.go`'s `invoke`, which already makes exactly this split for a callee's result count.
//
// Reported rather than silently clamped when there are too few values: that is #9's arity
// question arriving late, the same reading `branch` gives it.
func returnFrom(st *stack, results, refResults int) error {
	if len(st.num) < results {
		return fmt.Errorf("%w: return with %d values on the stack, but the function declares %d results",
			ErrNotValidated, len(st.num), results)
	}
	if len(st.refs) < refResults {
		return fmt.Errorf("%w: return with %d references on the stack, but the function declares %d reference results",
			ErrNotValidated, len(st.refs), refResults)
	}
	copy(st.num, st.num[len(st.num)-results:])
	st.num = st.num[:results]
	if st.tracking {
		copy(st.numSeq, st.numSeq[len(st.numSeq)-results:])
		st.numSeq = st.numSeq[:results]
	}
	copy(st.refs, st.refs[len(st.refs)-refResults:])
	st.refs = st.refs[:refResults]
	copy(st.refSeq, st.refSeq[len(st.refSeq)-refResults:])
	st.refSeq = st.refSeq[:refResults]
	return nil
}

// caught is catchThrown's report: whether an enclosing handler matched, and if so, whether the
// matched clause's branch is an ordinary label branch or names the implicit function-body
// label (a "return", opBr's own reading of `depth == len(ctrl)` reused here because a catch
// clause's LabelIndex is drawn from the identical space).
type caught struct {
	Matched  bool
	IsReturn bool
	PC       int
	Level    int
}

// raiseOrCatch is the one interception point every thrown-producing or thrown-propagating
// dispatch-loop site shares, rather than five copies of the same type-check-then-scan. `err` is
// whatever `opThrow`/`opThrowRef` just built, or whatever `call`/`callIndirect` returned from a
// callee's own `runFrame` — in every other case (a plain error, a `*Trap`) `errors.As` fails
// once and the caller's own `if !c.Matched { return err }` puts the original error straight
// back out, so a non-exception failure costs exactly one failed type assertion and nothing else
// changes about how it propagates.
func (in *Instance) raiseOrCatch(st *stack, ctrl []label, err error) (caught, error) {
	var t *Uncaught
	if !errors.As(err, &t) {
		return caught{}, nil
	}
	return in.catchThrown(st, ctrl, t)
}

// catchThrown scans ctrl from the top for the nearest handler label matching t's tag, and
// performs the reference's own branch on a match — `Handler`'s reduction rules, `eval.ml:1086-
// 1112`, walked outward one label at a time exactly as `Handler (n, cs, code')`'s fallthrough
// arm re-wraps around a nested step. `caught.Matched` false means no enclosing label in *this*
// ctrl caught it, and the caller re-propagates t unchanged, which is `call`'s ordinary `return
// err` path doing the "escapes this runFrame invocation" half of 0022's design (the Go-call-
// boundary crossing 0022 §2 names as the mechanism's whole point).
//
// **This is the genuinely new machinery** (Scott's own shaping order on 2c): nothing before
// this inspects ctrl on an error path, only on a branch's normal-path lookup. Falsification
// depth concentrates here.
func (in *Instance) catchThrown(st *stack, ctrl []label, t *Uncaught) (caught, error) {
	for i := len(ctrl) - 1; i >= 0; i-- {
		l := &ctrl[i]
		if !l.isHandler {
			continue
		}
		// **Clause order is the wire order, and the reference's own reduction is
		// first-match**: `Handler (n, {it = Catch (x1,x2); _} :: cs, ...)` peels the head
		// clause, tests it, and only recurses into `cs` (the rest) on a miss — so the first
		// clause whose kind and tag agree wins, never a "most specific" or "closest tag"
		// rule. A reader trying clauses out of order, or stopping at the first *tag match
		// regardless of kind* rather than the first *clause*, would disagree with the
		// reference on a try_table carrying more than one clause for the same tag (legal —
		// nothing in the grammar forbids it).
		for _, c := range l.catches {
			var isTagMatch bool
			switch c.Kind {
			case binary.CatchTag, binary.CatchTagRef:
				tg, err := in.tagFor(c.TagIndex)
				if err != nil {
					// The layering debt, read as "this clause never matches" rather than
					// surfaced: a malformed tag index inside a catch clause is #9's
					// question, and the exception is real and in flight regardless of
					// whether this particular clause can even be evaluated.
					continue
				}
				isTagMatch = t.exc.tag == tg
			case binary.CatchAny, binary.CatchAnyRef:
				// No tag to compare — these two match unconditionally, which the second
				// switch below (checking isTagMatch only for the Tag/TagRef pair) already
				// treats correctly by never testing it for these two kinds.
			}
			switch c.Kind {
			case binary.CatchTag, binary.CatchTagRef:
				if !isTagMatch {
					continue
				}
			case binary.CatchAny, binary.CatchAnyRef:
				// Matches unconditionally.
			}
			// **Discard everything above this handler's own base before pushing the
			// payload** — `is_jumping e' -> vs, [e']` (eval.ml:1059-1060) drops the ambient
			// `vs` at every `Label` an exception unwinds past, keeping only what the
			// exception itself carries. Without this, a `try_table` body that pushed any
			// scratch before throwing (or even just consumed its own blocktype params) would
			// leave that scratch on the stack under the restored payload — invisible on a
			// throw with no ambient stack and a corruption on any other, which is exactly
			// the shape a body-with-side-effects-then-throw vector would need to catch and
			// this rung's own falsification depth is ordered to test for.
			st.num = st.num[:l.height]
			if st.tracking {
				st.numSeq = st.numSeq[:l.height]
			}
			st.refs = st.refs[:l.refHeight]
			st.refSeq = st.refSeq[:l.refHeight]
			switch c.Kind {
			case binary.CatchTag:
				pushPayload(st, t.exc)
			case binary.CatchTagRef:
				pushPayload(st, t.exc)
				st.pushRef(ref{Exc: t.exc})
			case binary.CatchAnyRef:
				st.pushRef(ref{Exc: t.exc})
			case binary.CatchAny: // no payload pushed
			}
			return in.branchTo(st, ctrl, i, c.LabelIndex)
		}
		// No clause in this handler matched — `Handler (n, [], (vs', Throwing ...))` and the
		// cs-exhausted fallthrough both re-raise past this label, which is exactly "keep
		// scanning outward" in this loop's own shape (i-- continues to the next enclosing
		// label) rather than a distinct branch.
	}
	return caught{}, nil
}

// pushPayload pushes an exception's payload values back onto the stacks in declaration order —
// `vs0 @ vs` (eval.ml:1088,1094): `vs0` is the payload, prepended ahead of whatever the handler
// already had. Both arrays, independently, `call.go`'s own reason repeated at every stack-
// surgery site in this package: the two arrays have independent depths and a payload can mix
// kinds.
func pushPayload(st *stack, exc *excObj) {
	// **Through `pushNum`/`pushRef`, not a direct append** — 0023's sequence tracking is
	// maintained entirely inside those two functions (value.go), and a payload value re-entering
	// the stack needs a sequence number exactly like any other push, or `drop`'s own invariant
	// (every live slot has one, once tracking is on) breaks for the one value most likely to sit
	// directly under a `drop`: an exnref's own payload, restored by a `catch_ref` clause.
	for _, v := range exc.num {
		st.pushNum(v)
	}
	for _, r := range exc.refs {
		st.pushRef(r)
	}
}

// branchTo performs the stack surgery a matched catch clause's branch needs, sharing `branch`'s
// own truncation logic rather than duplicating it: a catch clause's label index is a plain
// branch depth **relative to the scope outside the try_table's own label** — `valid.ml:581-584`
// checks every clause against `c`, the context *before* `{c with labels = ts2 :: c.labels}`
// pushes the try_table's own label, so `ctrl[:handlerIdx]` (excluding the handler label itself)
// is the slice a clause's LabelIndex resolves against, exactly as if the handler label were
// never pushed at all.
func (in *Instance) branchTo(st *stack, ctrl []label, handlerIdx int, labelIdx uint32) (caught, error) {
	outer := ctrl[:handlerIdx]
	if int(labelIdx) == len(outer) {
		// The catch clause's label names the function body itself — legal, same reading
		// opBr's own function-body-is-an-implicit-label case gives `br len(ctrl)`.
		return caught{Matched: true, IsReturn: true}, nil
	}
	pc, level, err := in.branch(st, outer, uint64(labelIdx))
	if err != nil {
		return caught{}, err
	}
	return caught{Matched: true, PC: pc, Level: level}, nil
}
