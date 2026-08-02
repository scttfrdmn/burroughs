package text

// The flat instruction grammar: `plaininstr` (parser.mly:556-654) and the immediate readers
// it dispatches to. #63's stratum, whose seam with #64 is *defect ownership, not surface
// form* — so `expr1`'s minimal arm (:814) is here too, since it only transports the token
// stream to a defect living in one of these readers.
//
// **The dispatch is derived, not enumerated.** `plaininstr` has 83 arms, and hand-listing
// them would be the enumeration-is-a-sample failure with 83 chances to be wrong. Extracted
// from the reference, those 83 arms collapse to **16 immediate shapes** — `''`, `idx`,
// `idx idx`, `idx_opt offset_opt align_opt`, and so on — and the generated keyword table
// (decision 0009) already maps all 589 mnemonics to the reference's own token kind. So the
// only thing this file states is kind→shape, which is 16 rows of facts read off the grammar,
// and TestPlaininstrShapesMatchTheReference re-derives it from `parser.mly` at test time so a
// row that drifts is a failure rather than a silent divergence.
//
// That is the #53 dividend compounding: the table bought to answer `unknown operator` now
// schedules the whole instruction grammar.

// immShape is the immediate sequence an instruction takes after its mnemonic.
//
// Named for the reference's production names rather than for what the immediates mean, because
// the authority is `parser.mly` and a second vocabulary here would be a second declaration of
// the same set — the same argument decision 0009 makes for keywordKind.
type immShape uint8

const (
	immNone        immShape = iota // 24 arms: UNREACHABLE, NOP, DROP, BINARY, …
	immIdx                         // 23 arms: BR, CALL, LOCAL_GET, …
	immIdxIdx                      // 9 arms:  STRUCT_GET, ARRAY_COPY, …
	immIdxOpt                      // 8 arms:  MEMORY_SIZE, TABLE_GET, …
	immMemarg                      // 4 arms:  LOAD, STORE, VEC_LOAD, VEC_STORE
	immIdxIdxOpt                   // 2 arms:  MEMORY_COPY, TABLE_COPY
	immLaneImms                    // 2 arms:  VEC_LOAD_LANE, VEC_STORE_LANE
	immLaneIdx                     // 2 arms:  VEC_EXTRACT, VEC_REPLACE
	immReftype                     // 2 arms:  REF_CAST, REF_TEST
	immIdxIdxList                  // 1 arm:   BR_TABLE
	immIdxReftype2                 // 1 arm:   BR_ON_CAST
	immHeaptype                    // 1 arm:   REF_NULL
	immIdxNat32                    // 1 arm:   ARRAY_NEW_FIXED
	immNum                         // 1 arm:   CONST
	immVecConst                    // 1 arm:   VEC_CONST (VECSHAPE list(num))
	immLaneIdxList                 // 1 arm:   VEC_SHUFFLE (list(laneidx))
)

// plaininstrShapes maps a mnemonic's reference token kind to its immediate shape.
//
// **Two kinds appear under two shapes in the reference and are absent here on purpose.**
// `MEMORY_INIT` and `TABLE_INIT` each have an `idx idx` arm and an `idx` arm marked
// `/* Sugar */` (:607/:588) — the sugar arm defaults the first index to 0. A map cannot hold
// both, and picking one would silently reject the other; they are handled by
// shapeOf's explicit optional-second-idx case instead, which is why the extraction control
// checks them by name rather than by lookup.
var plaininstrShapes = map[keywordKind]immShape{
	// (no immediates) — parser.mly arms with an empty right-hand side
	"UNREACHABLE": immNone, "NOP": immNone, "DROP": immNone, "RETURN": immNone,
	"THROW_REF": immNone, "REF_IS_NULL": immNone, "REF_AS_NON_NULL": immNone,
	"REF_EQ": immNone, "REF_I31": immNone, "I31_GET": immNone, "ARRAY_LEN": immNone,
	"EXTERN_CONVERT": immNone, "TEST": immNone, "COMPARE": immNone, "UNARY": immNone,
	"BINARY": immNone, "CONVERT": immNone, "VEC_UNARY": immNone, "VEC_BINARY": immNone,
	"VEC_TERNARY": immNone, "VEC_TEST": immNone, "VEC_SHIFT": immNone,
	"VEC_BITMASK": immNone, "VEC_SPLAT": immNone,

	// idx
	"BR": immIdx, "BR_IF": immIdx, "BR_ON_NULL": immIdx, "CALL": immIdx,
	"CALL_REF": immIdx, "RETURN_CALL": immIdx, "RETURN_CALL_REF": immIdx,
	"THROW": immIdx, "LOCAL_GET": immIdx, "LOCAL_SET": immIdx, "LOCAL_TEE": immIdx,
	"GLOBAL_GET": immIdx, "GLOBAL_SET": immIdx, "ELEM_DROP": immIdx,
	"DATA_DROP": immIdx, "REF_FUNC": immIdx, "STRUCT_NEW": immIdx,
	"ARRAY_NEW": immIdx, "ARRAY_GET": immIdx, "ARRAY_SET": immIdx,
	"ARRAY_FILL": immIdx,

	// idx idx
	"STRUCT_GET": immIdxIdx, "STRUCT_SET": immIdxIdx, "ARRAY_COPY": immIdxIdx,
	"ARRAY_NEW_ELEM": immIdxIdx, "ARRAY_NEW_DATA": immIdxIdx,
	"ARRAY_INIT_DATA": immIdxIdx, "ARRAY_INIT_ELEM": immIdxIdx,

	// idx_opt
	"TABLE_GET": immIdxOpt, "TABLE_SET": immIdxOpt, "TABLE_SIZE": immIdxOpt,
	"TABLE_GROW": immIdxOpt, "TABLE_FILL": immIdxOpt, "MEMORY_SIZE": immIdxOpt,
	"MEMORY_GROW": immIdxOpt, "MEMORY_FILL": immIdxOpt,

	// idx_opt offset_opt align_opt — the memarg shape, and #63's largest bucket
	"LOAD": immMemarg, "STORE": immMemarg, "VEC_LOAD": immMemarg, "VEC_STORE": immMemarg,

	// the singletons and small groups
	"MEMORY_COPY": immIdxIdxOpt, "TABLE_COPY": immIdxIdxOpt,
	"VEC_LOAD_LANE": immLaneImms, "VEC_STORE_LANE": immLaneImms,
	"VEC_EXTRACT": immLaneIdx, "VEC_REPLACE": immLaneIdx,
	"REF_CAST": immReftype, "REF_TEST": immReftype,
	"BR_TABLE":        immIdxIdxList,
	"BR_ON_CAST":      immIdxReftype2,
	"REF_NULL":        immHeaptype,
	"ARRAY_NEW_FIXED": immIdxNat32,
	"CONST":           immNum,
	"VEC_CONST":       immVecConst,
	"VEC_SHUFFLE":     immLaneIdxList,
}

// initSugarKinds are the two kinds whose second index is optional: `memory.init` and
// `table.init` each have an `idx idx` arm and an `idx` sugar arm defaulting the first
// index (parser.mly:588, :607). Held apart from plaininstrShapes because a map cannot
// express "either", and the drift control asserts these are exactly the kinds the
// reference gives two arms.
var initSugarKinds = map[keywordKind]bool{
	"MEMORY_INIT": true,
	"TABLE_INIT":  true,
}

// shapeOf resolves a keyword token to its immediate shape.
//
// The optional-first-index kinds are answered here rather than from the map, because their two
// arms are a *choice* the map cannot hold: `memory.init x y` and `memory.init y`. The caller
// reads one idx and then another only if one is there, which is what `idx idx | idx` means.
func shapeOf(k keywordKind) (immShape, bool) {
	if initSugarKinds[k] {
		return immIdxIdxOpt, true
	}
	s, ok := plaininstrShapes[k]
	return s, ok
}

// expr1NonPlainLeaders are `expr1`'s arms whose leader is *not* a `plaininstr` mnemonic
// (parser.mly:813-834).
//
// `expr1` has ten arms. The first is `plaininstr expr_list`, whose leaders are the 589 mnemonics
// the generated table already knows and `shapeOf` already answers. The other nine lead with one of
// these seven tokens — SELECT, CALL_INDIRECT and RETURN_CALL_INDIRECT each having a sugar arm that
// shares its leader. So `plaininstr`'s domain plus these seven **is** the set of keywords a folded
// instruction can begin with, which is exactly what startsInstruction needs.
//
// Enumerated here, and the enumeration is legitimate for once: *the reference enumerates it*. This
// is not a sample of a set whose membership is computed — it is seven arm heads written literally
// in the grammar, transcribed. What makes the transcription trustworthy is that
// TestExpr1LeadersMatchTheReference re-extracts the same arms from `parser.mly` and fails on drift,
// per the authority-for-accept-direction-facts rule: an upstream arm added or a leader renamed
// makes this list wrong in the direction no vector can see, so a machine re-reads the authority
// rather than a reviewer re-reading my list.
//
// BLOCK, LOOP, IF and TRY_TABLE appear here *and* in `blockinstr` (:726-738). Not a duplicate: the
// flat form ends at END and the folded form ends at the closing paren, two productions sharing a
// keyword. Both start an instruction, which is the only question this set answers.
var expr1NonPlainLeaders = map[keywordKind]bool{
	kwSelect:             true, // :815, with selectexpr_results
	kwCallIndirect:       true, // :817 and :819, the second defaulting the table index
	kwReturnCallIndirect: true, // :821 and :823, likewise
	kwBlock:              true, // :826
	kwLoop:               true, // :828
	kwIf:                 true, // :830, with if_block rather than block
	kwTryTable:           true, // :833
}

// startsInstruction reports whether a keyword can begin an instruction — flat or folded.
//
// **The set is derived, not listed:** `shapeOf`'s domain is `plaininstr`'s 83 arms via the
// generated keyword table, and expr1NonPlainLeaders is the reference's own seven-arm remainder. So
// the predicate grows when the grammar does, rather than freezing at the moment of authorship —
// *scope controls to the space*, and the space here is "every leader `instr1` has".
//
// Its purpose is the boundary's honesty. `bodyBoundary` reports *unimplemented* to mean "a later
// stratum will read this", and that claim is only true for a token some production can actually
// start with. `(func (local i32) (param i32))` contains no instructions at all, so promising an
// instruction-body reader for its `(param` was the wrong-layer error in the flattering direction:
// a module the reference rejects on the merits, parked in #64's bucket as though finishing #64
// would make it legal. Twelve `func.wast` field-ordering vectors turned on exactly that (#70).
func startsInstruction(k keywordKind) bool {
	if _, ok := shapeOf(k); ok {
		return true
	}
	return expr1NonPlainLeaders[k]
}

// plaininstr parses one flat instruction: a mnemonic and its immediates (parser.mly:556-654).
//
// Returns false without consuming anything when the cursor is not on a mnemonic this table
// knows, so `instr1` can try `blockinstr` and `expr` instead. A production that reports "not
// mine" must leave the cursor where it found it, or the caller's alternative starts mid-token.
func (p *parser) plaininstr() (bool, error) {
	t := p.c.peek()
	if t.Kind != KeywordTok {
		return false, nil
	}
	shape, ok := shapeOf(t.Keyword)
	if !ok {
		return false, nil
	}
	p.c.next()
	return true, p.immediates(shape, t)
}

// immediates reads the immediate sequence for one shape.
//
// Each arm is the reference's production read literally, and the *order* of the reads is
// load-bearing where a shape has two of them: `align` runs after `offset`, because
// `idx_opt offset_opt align_opt` (parser.mly:596) is written that way and the lexer gives
// `offset=` and `align=` distinct token kinds — so a module writing them backwards is a syntax
// error the reference reports, not a set to be matched in any order.
func (p *parser) immediates(shape immShape, mnemonic Token) error {
	switch shape {
	case immNone:
		return nil
	case immIdx:
		return p.idx()
	case immIdxIdx:
		if err := p.idx(); err != nil {
			return err
		}
		return p.idx()
	case immIdxOpt:
		_, err := p.idxOpt()
		return err
	case immIdxIdxOpt:
		// `idx_idx_opt` (:494) is empty-or-two, and the `memory.init`/`table.init` sugar arms
		// are one-or-two. Both are served by "read as many as are there", which is why the
		// second read is conditional rather than required after the first.
		present, err := p.idxOpt()
		if err != nil {
			return err
		}
		if present {
			_, err = p.idxOpt()
		}
		return err
	case immMemarg:
		return p.memarg()
	case immLaneImms:
		// `lane_imms` (:661) is the memarg shape with a mandatory trailing laneidx, spelled out
		// upstream as four arms "to avoid spurious conflicts" rather than as a composition.
		if err := p.memarg(); err != nil {
			return err
		}
		return p.laneidx()
	case immLaneIdx:
		return p.laneidx()
	case immReftype:
		// The four value-returning readers are called for their errors here and their results
		// discarded: an instruction's immediate is never a comparison operand — only a functype's
		// value types reach `inline_functype_explicit`. Named once for the whole switch rather than
		// at each of the four sites.
		_, err := p.reftype()
		return err
	case immIdxIdxList:
		// `br_table` takes one idx then `idx_list` (:497), whose empty arm is why the loop's
		// exit is "no idx here" rather than an error. idxList is #62's and already loops on
		// exactly that lookahead.
		if err := p.idx(); err != nil {
			return err
		}
		return p.idxList()
	case immIdxReftype2:
		if err := p.idx(); err != nil {
			return err
		}
		if _, err := p.reftype(); err != nil {
			return err
		}
		_, err := p.reftype()
		return err
	case immHeaptype:
		_, err := p.heaptype()
		return err
	case immIdxNat32:
		if err := p.idx(); err != nil {
			return err
		}
		return p.nat32()
	case immNum:
		return p.constImm(mnemonic)
	case immVecConst:
		return p.vecConst(mnemonic)
	case immLaneIdxList:
		return p.laneIdxList(mnemonic)
	}
	// Unreachable while shapeOf only returns the constants above, and a panic rather than a
	// silent nil because the alternative is an instruction accepting no immediates by falling
	// through — an accept-direction defect no vector can see. *An error constant with no
	// reachable path is a missing check wearing a disguise*; so is a default case that shrugs.
	panic("text: unhandled immShape — a shape was added without a reader")
}

// atIdx reports whether the cursor is on something that can start an `idx` (parser.mly:487).
//
// The lookahead that makes every `_opt` arm above a decision rather than a parse: only NAT and
// VAR begin an idx, so anything else is the empty arm and the cursor must not move.
func (p *parser) atIdx() bool { return p.c.at(NatTok) || p.c.at(VarTok) }

// idxOpt parses `idx_opt` (:491): an idx if one is there, otherwise the empty arm.
//
// Returns whether one was present *and* any error from parsing it, rather than folding the two
// into one value. The first draft stashed the error on the parser and returned only the bool,
// which is a second channel for a verdict that already has one — and worse, a caller that
// ignored the bool would silently drop a width error. Two return values make dropping one a
// compile error at the call site.
func (p *parser) idxOpt() (bool, error) {
	if !p.atIdx() {
		return false, nil
	}
	return true, p.idx()
}

// memarg parses `idx_opt offset_opt align_opt` (:596), the load/store immediates.
//
// The three are separately optional and *ordered*: `offset=` before `align=`, because that is
// how the production is written and the lexer hands back distinct kinds for the two. So
// `i32.load align=4 offset=0` is malformed, and this reports it by leaving `offset=` unconsumed
// for the caller's `unexpected token`.
func (p *parser) memarg() error {
	if _, err := p.idxOpt(); err != nil {
		return err
	}
	if p.c.at(OffsetEqNat) {
		t := p.c.next()
		// `offset_` is `nat64` (:526) — 64 bits regardless of the memory's address type, which
		// is the width the *field* declares rather than the width the module uses. *When two
		// fields disagree about a value, the suite has handed you a bidirectional control.*
		if _, ok := parseNat(offsetEqValue(t.Text), 64); !ok {
			return errAt(t, "i64 constant out of range")
		}
	}
	if p.c.at(AlignEqNat) {
		t := p.c.next()
		pow2, isNat := parseAlign(t.Text)
		if !isNat {
			return errAt(t, "i64 constant out of range")
		}
		if !pow2 {
			return errAt(t, "alignment must be a power of two")
		}
	}
	return nil
}

// laneidx parses `laneidx` (:658), which is `nat8` — the 15 `i8 constant out of range` vectors.
func (p *parser) laneidx() error {
	t := p.c.peek()
	if t.Kind != NatTok {
		return p.unexpected()
	}
	p.c.next()
	if _, ok := parseNat(t.Text, 8); !ok {
		return errAt(t, "i8 constant out of range")
	}
	return nil
}

// nat32 parses `nat32` (parser.mly:478), a bare NAT range-checked at 32 bits.
//
// Distinct from idx even though both are `nat32`-checked: idx also admits a VAR, and
// `array.new_fixed`'s second immediate is a *count*, which has no symbolic spelling.
func (p *parser) nat32() error {
	t := p.c.peek()
	if t.Kind != NatTok {
		return p.unexpected()
	}
	p.c.next()
	if _, ok := parseNat(t.Text, 32); !ok {
		return errAt(t, "i32 constant out of range")
	}
	return nil
}

// constImm parses `CONST num` (:636) — and the *width is the mnemonic's*, not the token's.
//
// `i32.const`, `i64.const`, `f32.const` and `f64.const` all lex to one CONST kind carrying a
// conversion closure (lexer.mll:308-319), so the reference recovers the width from which arm
// built the token. This reader has the mnemonic's text instead, which is the same information
// spelled differently — and it is the reason immNum's arm is passed the mnemonic token at all.
//
// Being wrong here is invisible in one direction: reading i64.const at 32 bits would reject
// valid modules, and reading i32.const at 64 bits would accept 33-bit constants the reference
// refuses. Only the second has vectors, which is why the first is stated as the risk.
func (p *parser) constImm(mnemonic Token) error {
	bits, isFloat := constWidth(mnemonic.Text)
	if bits == 0 {
		// A CONST mnemonic the width table does not know. Not reachable from the generated
		// keyword table's four `*.const` spellings, and a hard error rather than a default width
		// because a default is how a fifth spelling would arrive silently parsed at the wrong
		// width — the accept-direction failure no vector reports.
		return errAt(mnemonic, "unexpected token")
	}
	t := p.c.peek()
	if t.Kind != NatTok && t.Kind != IntTok && t.Kind != FloatTok {
		// `num` is exactly those three arms (parser.mly:482-485), so anything else is a syntax
		// error from the *production*, not a range failure. `i32.const nan:arithmetic`
		// (i32.wast:979) is the vector: `nan:arithmetic` lexes to NAN (lexer.mll:804), which no
		// `num` arm admits, and the expected string is `unexpected token` rather than `constant
		// out of range`. Two error strings that would otherwise be easy to conflate, and the
		// suite distinguishes them.
		return p.unexpected()
	}
	p.c.next()
	return p.checkNumRange(t, bits, isFloat)
}

// checkNumRange applies the width check for one `num` token at a known width.
//
// Split out of constImm because vecConst needs the identical check at a width that comes from
// the *shape* instead of the mnemonic, and both call sites answer with the same string. Sharing
// the check rather than the caller is what keeps the two width *sources* distinct while the
// range rule stays one thing.
func (p *parser) checkNumRange(t Token, bits uint, isFloat bool) error {
	if isFloat {
		// **A float const accepts all three `num` arms**, and the range check is the same for
		// each: `f32.const 1` is legal (`align64.wast:282` writes exactly that), and so is
		// `f32.const -1`. The first draft returned nil for NatTok/IntTok on the grounds that "an
		// integer literal cannot overflow a float", which is false — `f32.const
		// 340282356779733661637539395458142568448` is a NAT, and `const.wast:349` expects it
		// rejected. `is_inf` (fxx.ml:323) does not care which arm produced the digits.
		if !fitsAsFloatConst(t.Text, bits) {
			return errAt(t, "constant out of range")
		}
		return nil
	}
	if t.Kind == FloatTok {
		// `i32.const 1.5` — the reference fails in `I32.of_string`'s `dec_digit` on the `.` and
		// reports it through `num f s` (parser.mly:53) as this string, not as a syntax error.
		// The token *is* a legal `num`, so the production accepts it and the conversion refuses
		// it: which is why this is a range error and the NAN case above is a syntax error.
		return errAt(t, "constant out of range")
	}
	if !fitsAsIntConst(t.Text, bits) {
		return errAt(t, "constant out of range")
	}
	return nil
}

// vecConst parses `VECSHAPE list(num)` (:642), the `v128.const` immediates.
//
// Two error strings come out of one production, from the two exception arms of `vec`
// (parser.mly:57-59): `Invalid_argument` → `wrong number of lane literals`, raised by
// `of_strings`' own `if List.length ss <> num_lanes shape then invalid_arg` (v128.ml:500-501),
// and `Failure` → `constant out of range`, raised by the per-lane converter.
//
// **The count is checked first, and that ordering is the reference's, read off `of_strings`
// rather than guessed.** The length test is the function's first statement, *before* the
// `List.iteri` that converts any lane — so a list that is both the wrong length and full of
// out-of-range literals reports the length. `simd_const.wast:480` is precisely that vector:
// `v128.const i32x4 0x10000000000000000 0x10000000000000000` — two literals where four are
// wanted, each far past 32 bits — and it expects `wrong number of lane literals`. An
// implementation converting as it reads reports `constant out of range` and fails it.
//
// The first draft of this function had the comment above stating the opposite ordering, with a
// rationale ("the reference reads all of them, then constructs the vector") that was a plausible
// reading of the *grammar* and wrong about the *function*. `list(num)` does collect the tokens
// first; the conversion those tokens feed is what carries both checks, and it length-tests
// first. That is the 0003 LEB shape again — the defect stated as the rule, and refuted here by
// an oracle-covered vector rather than by review.
//
// **But "first" means first among `vec`'s two arms, not first of everything — a syntax error on an
// illegal follower still precedes both.** Found by sweeping laneIdxList's grave for siblings of the
// same shape rather than by a vector, because the suite has none: `v128.const i8x16 0 … 14 $x`
// reported `wrong number of lane literals`, and a VAR is not a `num` (:476-478), so the reference
// cannot reduce `VECSHAPE list(num)` with `$x` in the lookahead and never reaches `vec` at all.
// The asymmetry that made laneIdxList's fix insufficient here is exactly the one this comment
// already noted from the other side: `num` admits NAT, INT and FLOAT, so the *only* illegal
// followers are the kinds outside all three, and VAR is the reachable one.
//
// Scoped like laneIdxList's arm and with the same caveat: VarTok is the follower a `list(num)` can
// meet in practice, and the honest general fix is Follow(instr), which waits on #64. Anything else
// illegal here is still misreported as a count error — a wrong message on a module both readers
// reject, never an acceptance.
func (p *parser) vecConst(mnemonic Token) error {
	t := p.c.peek()
	if t.Kind != KeywordTok || t.Keyword != kwVecshape {
		// `v128.const 0 0 0 0` (simd_const.wast:236) and bare `v128.const` (:231) both expect
		// `unexpected token`: the shape is a required token of the production, so its absence is
		// the parser's failure and never a lane-count one.
		return p.unexpected()
	}
	shape := p.c.next()
	lanes, bits, isFloat, ok := vecShapeLanes(shape.Text)
	if !ok {
		// Unreachable from the generated table's six VECSHAPE spellings, and an error rather
		// than a default shape because a default is how a seventh would arrive silently read at
		// the wrong lane width.
		return p.unexpectedAt(shape)
	}

	// Collected before any conversion, because the length test comes first. The tokens are kept
	// rather than counted so the per-lane errors can still be reported *at the offending lane* —
	// the count check needs all of them, the range check needs each of them, and only one of
	// those two orders is the reference's.
	var lits []Token
	for p.c.at(NatTok) || p.c.at(IntTok) || p.c.at(FloatTok) {
		lits = append(lits, p.c.next())
	}
	// The follower, before the count — see the header. `vec`'s length test is first among *its*
	// two arms, and both of them are downstream of the automaton reducing the production at all.
	if p.c.at(VarTok) {
		return p.unexpected()
	}
	if len(lits) != lanes {
		// Reported at the mnemonic because `vec`'s arm is `error (at $sloc)` (parser.mly:59) and
		// `$sloc` spans the whole production — the offence is the list's length, which is not
		// located at any one token.
		return errAt(mnemonic, "wrong number of lane literals")
	}
	for _, lit := range lits {
		if err := p.checkNumRange(lit, bits, isFloat); err != nil {
			return err
		}
	}
	return nil
}

// laneIdxList parses `list(laneidx)` (:651), `i8x16.shuffle`'s sixteen indices.
//
// Sixteen exactly, and the message is `wrong number of lane indices` — a *different* string from
// vecConst's `…lane literals`, and the suite has both. Reported at the mnemonic because the
// reference's arm is `error (at $sloc)` (parser.mly:653), spanning the production.
//
// **Here the count is checked *last*, which is the opposite of vecConst — and the difference is
// the reference's structure, not an inconsistency.** `laneidx` is `nat8` (:658), so each index's
// range check happens *inside the grammar* as menhir reduces it, and the length test is a
// semantic action running after the whole list reduces (:652). vecConst's length test lives in
// `of_strings`, ahead of its converter. Same two questions, opposite orders, because they sit in
// different layers of the reference.
//
// The suite pins the difference rather than leaving it to reading: `simd_lane.wast:526` writes
// sixteen indices with the last one `256` — correct count, one bad index — and expects `i8
// constant out of range`; `:519` writes seventeen good ones and expects `wrong number of lane
// indices`. What makes this a real distinction from vecConst is `:522`, sixteen with `-1` last:
// `-1` is an IntTok, not a NAT, so it does not match `laneidx` *at all*, the list stops at
// fifteen, and the expected string is **`unexpected token`** — the trailing token is what fails,
// not the count. So the loop's lookahead must be NAT-only: an IntTok arm here would turn that
// vector's verdict into a count error.
//
// # The count is outside; range and syntax are both positional (grave, #63)
//
// NAT-only lookahead was necessary and not sufficient, and the first draft stopped there: it left
// the list at fifteen as intended and then *reported the count*, because `n != 16` was the next
// statement. Six vectors say otherwise — `:522` (`-1`), `:604`/`:608`/`:612`/`:616` (`15.0`,
// `0.5`, `-inf`, `inf`) all expect `unexpected token` and got `wrong number of lane indices`.
//
// The cause is LR reduction order, which is the part of "the reference's structure" the comment
// above had only half of. `error (at $sloc)` at :653 is a **semantic action**, so it cannot run
// until the parser *reduces* `VEC_SHUFFLE list(laneidx)` — and the state after the list can still
// shift a NAT, so it has no default reduction and must consult the lookahead first. A lookahead
// outside the follow set is a syntax error raised in the automaton, before any action of the
// production it would have completed. So the count is genuinely *outside* both other checks.
//
// **The other two are not ordered with respect to each other, and the first version of this
// comment claimed they were.** It called the structure "three-deep — range, then syntax, then
// count", which reads as a precedence and is not one: `nat8`'s range error and the automaton's
// syntax error are both raised *at a token position* as the input is consumed left to right, so
// whichever fault comes first in the source wins. Printed rather than reasoned about, which is
// what caught it: `256 … -1` is a range error and `-1 … 256` is a syntax error, same two faults,
// order decided by position alone. The claim survived because every cited vector has exactly one
// fault, so no vector could distinguish a precedence from a scan order — *a control scoped to the
// current sample inherits the current blind spot*, and here the sample was the whole suite.
//
// So the real shape is two layers, not three: **positional faults (range or syntax, leftmost
// first) → the count (the action, after the list reduces)**. The loop below gets that for free by
// being a left-to-right scan; the only thing it must not do is check the count first.
//
// This is the *deferred* cousin of vecConst's ordering, and worth stating because the two
// productions differ for two independent reasons. vecConst's count lives in `of_strings`, reached
// through `fun c -> fst (vec …)` (:642) — a closure applied after parsing, so its count is later
// still. It escapes this defect only because `list(num)` admits INT and FLOAT, so the tokens that
// are illegal followers here are legal *members* there. Same asymmetry read from the other side:
// `num` ⊃ `laneidx`, and the difference lands in the follow position.
//
// Scoped to the certain subset rather than to a guessed follow set. IntTok and FloatTok are
// exactly the `num` kinds `laneidx` rejects, and no production admits a bare int or float in
// instruction position — `num` occurs only *inside* `CONST num` and `VEC_CONST VECSHAPE
// list(num)`, both of which consume it within their own plaininstr. Other illegal followers
// (a stray VAR, a misplaced `end`) would still be reported as count errors here; that is a
// wrong *message* on a module both readers reject, never an acceptance, and no vector covers it.
// Named rather than fixed by enumeration, because the honest fix is Follow(instr), which needs
// #64's blockinstr arms to exist before it can be written down.
func (p *parser) laneIdxList(mnemonic Token) error {
	n := 0
	for p.c.at(NatTok) {
		if err := p.laneidx(); err != nil {
			return err
		}
		n++
	}
	if p.c.at(IntTok) || p.c.at(FloatTok) {
		return p.unexpected()
	}
	if n != 16 {
		return errAt(mnemonic, "wrong number of lane indices")
	}
	return nil
}
