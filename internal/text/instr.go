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

// dataRefKinds are the mnemonics whose free variables include the **data index space**, which is
// section 12's emission condition (#8).
//
// Read off `free.ml` rather than off this file's own `idxLookupKinds`, and the two disagree in a way
// that matters. `free.ml` names four arms — `MemoryInit (x,y)` (:165), `DataDrop x` (:166),
// `ArrayNewData (x,y)` (:175), `ArrayInitData (x,y)` (:181) — but `idxLookupKinds` records each arm's
// **first** category, and for the two array arms that is `catType`. So `cat == catData` finds two of
// the four, and a module whose only data reference is an `array.init_data` would be emitted without
// section 12: well-formed text, and an image the decoder rejects. The condition is a property of the
// *instruction*, not of one of its indices, so it is keyed by mnemonic.
//
// This is the same set `internal/binary/instr.go`'s `dataRefOps` holds, one grammar over, and both
// were derived from those four `free.ml` lines rather than from the two the suite exercises
// (`binary.wast:302`, `:325` cover the `fc` pair; the `fb` pair has no vector at all).
// TestDataRefKindsMatchTheDecodersOpcodes holds the two sides to one authority, because a set that
// grows on one side of a round trip and not the other is an accept-direction defect on whichever
// side stayed still.
//
// Held as a set of kinds rather than of opcodes because that is the key this grammar has: the text
// side reaches the mnemonic and the binary side reaches the byte pair, so the agreement is checked
// through the generated opcode table rather than asserted by matching literals.
var dataRefKinds = map[keywordKind]bool{
	"MEMORY_INIT":     true, // fc 08 — free.ml:165
	"DATA_DROP":       true, // fc 09 — free.ml:166
	"ARRAY_NEW_DATA":  true, // fb 09 — free.ml:175
	"ARRAY_INIT_DATA": true, // fb 12 — free.ml:181
}

// labelTakingKinds are the `plaininstr` mnemonics whose **first** index is a label — the arms
// that pass `label` as `idx`'s lookup function (#80).
//
// The reference makes the lookup category a *parameter* of `idx` (parser.mly:487-489):
//
//	idx :
//	  | NAT { fun c lookup -> nat32 $1 $sloc @@ $sloc }
//	  | VAR { fun c lookup -> lookup c (var $1 $sloc) @@ $sloc }
//
// and each arm supplies one — `br ($2 c label)`, `call ($2 c func)`, `local_get ($2 c local)`
// (:561-620). So a faithful reader threads a category through every index. **Only labels are here,
// and the reason is a measurement rather than a preference:** across all 253 suite files exactly two
// vectors name an unbound symbolic index, and both are `unknown label` (token.wast:105, :121). Every
// other `unknown *` vector is either numeric — the NAT arm is `nat32 $1`, a width check with no
// lookup, so all 13 `assert_invalid "unknown label"` are `(br 1)`-shaped and validation's — or names
// something the module does bind later (`global.wast:668`'s forward `$g2`), which needs #64's
// deferred phase. Labels are separable precisely because their scope is *lexical*: there is no
// forward reference to wait for, so resolution can happen where the name is read. See #80.
//
// **Derived from the reference, not enumerated by hand**: TestLabelTakingArmsMatchTheReference
// extracts the arms passing `label` out of `plaininstr` and requires this set to equal them, so an
// upstream arm gaining or losing a label index fails the board rather than sitting unnoticed. All
// five take the label as their first index (`$2 c label`), which is why this is a set of kinds and
// not a set of positions — `BR_TABLE`'s *rest* are labels too, which idxList handles, and
// `BR_ON_CAST`'s later immediates are reftypes.
var labelTakingKinds = map[keywordKind]bool{
	"BR":         true,
	"BR_IF":      true,
	"BR_TABLE":   true,
	"BR_ON_NULL": true,
	"BR_ON_CAST": true,
}

// idxCategory is the index space an instruction's index immediate resolves against.
//
// The reference makes this a *parameter* of `idx` (parser.mly:487-489) and each arm supplies one —
// `call ($2 c func)`, `local_get ($2 c local)`, `memory_size ($2 c memory)`. A reader that ignores
// the category resolves `$x` in whichever space it happens to consult, which for a symbolic index
// silently produces a *different instruction*: accept-direction, and no vector reports it, because
// every `unknown <space>` vector in the corpus is an `assert_invalid` with a numeric index.
type idxCategory uint8

const (
	catNone idxCategory = iota
	catLocal
	catFunc
	catMemory
	catLabel
	catType
	catTag
	catGlobal
	catTable
	catData
	catElem

	// catFieldOfType is `struct.get`/`struct.set`'s second index: the field space **of the struct
	// type the first index names**, not a module-level space at all (`field` at parser.mly:162 is
	// `Lib.List32.nth c.types.fields x`). It is in the enum because `idxPairLookupKinds` must be able
	// to *name* it — the alternative is a missing row, which reads as an omission rather than as a
	// space this stratum does not have (#6). `idxSpaceFor` returns nil for it, which is a refusal.
	//
	// Never a *first* category, so `refCategoryNames` has no entry and the single-index control never
	// meets it. That asymmetry is deliberate and is asserted rather than assumed — see
	// TestFieldOfTypeIsNeverAFirstCategory.
	catFieldOfType
)

// idxLookupKinds is the lookup category each `plaininstr` arm's **first** index takes.
//
// Transcribed from the 47 arms of `plaininstr` that pass one, and machine-checked against them by
// TestIdxLookupKindsMatchTheReference — the same discipline `labelTakingKinds` is held to, and for
// the same reason: this is an accept-direction fact about which space a name resolves in, so the
// authority has to be the grammar rather than a reading of it.
//
// **Scoped to the space, not to the slice** (#33's widening rule): all 47 arms are listed, including
// the ten categories the code section cannot encode yet, because a control covering only today's
// three would freeze at the moment of authorship and say nothing about the next arm added. What the
// encoder does with a category it cannot resolve is refuse — see `idxSpaceFor`.
//
// The *second* index of a two-index arm is not here. Four arms take two categories
// (`array.init_data` is `type_` then `data`, `table.init` is `elem` and a defaulted `table`), and
// they are all in tiers this section does not reach; their second category is handled where those
// arms are implemented, and the drift control asserts the first-index mapping only, saying so.
var idxLookupKinds = map[keywordKind]idxCategory{
	"ARRAY_COPY": catType, "ARRAY_FILL": catType, "ARRAY_GET": catType,
	"ARRAY_INIT_DATA": catType, "ARRAY_INIT_ELEM": catType, "ARRAY_NEW": catType,
	"ARRAY_NEW_DATA": catType, "ARRAY_NEW_ELEM": catType, "ARRAY_NEW_FIXED": catType,
	"ARRAY_SET": catType, "STRUCT_GET": catType, "STRUCT_NEW": catType, "STRUCT_SET": catType,
	"CALL_REF": catType, "RETURN_CALL_REF": catType,

	"BR": catLabel, "BR_IF": catLabel, "BR_ON_CAST": catLabel, "BR_ON_NULL": catLabel,
	"BR_TABLE": catLabel,

	"CALL": catFunc, "RETURN_CALL": catFunc, "REF_FUNC": catFunc,

	"LOCAL_GET": catLocal, "LOCAL_SET": catLocal, "LOCAL_TEE": catLocal,

	"GLOBAL_GET": catGlobal, "GLOBAL_SET": catGlobal,

	"LOAD": catMemory, "STORE": catMemory, "VEC_LOAD": catMemory, "VEC_STORE": catMemory,
	"MEMORY_COPY": catMemory, "MEMORY_FILL": catMemory, "MEMORY_GROW": catMemory,
	"MEMORY_SIZE": catMemory,

	"TABLE_COPY": catTable, "TABLE_FILL": catTable, "TABLE_GET": catTable,
	"TABLE_GROW": catTable, "TABLE_SET": catTable, "TABLE_SIZE": catTable,

	// **The two sugar arms' written index is not the space their name suggests.**
	// `memory.init x y` is `memory_init ($2 c memory) ($3 c data)` (:607) and its sugar arm
	// `MEMORY_INIT idx` is `memory_init (0l @@ $loc($1)) ($2 c data)` (:609) — so when one index
	// is written it is the **data** index and the memory defaults to 0. `table.init` is the same
	// shape with `elem` (:587 and :589). Getting this backwards encodes a legal module that does
	// something else, which is why it is called out rather than left to the table's shape.
	//
	// These four line numbers were `:588` and `:607` — the two-index arm's line for one kind and
	// nothing in particular for the other — until TestIdxLookupKindsMatchTheReference was written
	// against this row and the sugar arms had to be located exactly. The prose was right and its
	// citations were not, which is the same class the fixture-provenance check exists for: a
	// citation nobody resolves is a claim.
	"MEMORY_INIT": catData, "TABLE_INIT": catElem,

	"DATA_DROP": catData, "ELEM_DROP": catElem,

	"THROW": catTag,
}

// initReversedKinds are the mnemonics whose two index immediates are encoded in the **reverse** of
// the written order.
//
// Read off `encode.ml`, which is the only authority for it — the *grammar* says which space each
// written index resolves in and says nothing at all about emission order, so this fact lives in a
// different file from `idxPairLookupKinds` and needs its own control. Four arms in the reference
// reverse:
//
//	CallIndirect       (x, y) -> op 0x11;          idx y; idx x   (:275)
//	ReturnCallIndirect (x, y) -> op 0x13;          idx y; idx x   (:278)
//	TableInit          (x, y) -> op 0xfc; 0x0cl;   idx y; idx x   (:294)
//	MemoryInit         (x, y) -> op 0xfc; 0x08l;   idx y; idx x   (:411)
//
// and the seven other two-index arms (`TableCopy`, `MemoryCopy`, and the five `fb` array forms) do
// not. Only two are here: the `call_indirect` pair does not route through `retainIdxPair` at all,
// its immediates being built by `callIndirectImm`'s patch because a typeuse must be interned in
// stage 2 — and *that* function already reverses, with its own citation to these same lines. Two
// places knowing one fact is the #78/#105/#106 shape, so the drift control names all four reversing
// arms and states which mechanism carries each; the alternative (routing `call_indirect` through
// here) would put a patch-shaped resolution behind a cursor-shaped API for the sake of a set.
//
// **Scoped to the space** by the control rather than by this map: the test extracts every
// `idx <var>; idx <var>` arm from `encode.ml` and requires the reversing ones to be exactly these
// four, so an upstream arm that starts or stops reversing fails the board.
var initReversedKinds = map[keywordKind]bool{
	"TABLE_INIT":  true,
	"MEMORY_INIT": true,
}

// idxPair is the two lookup categories a two-index `plaininstr` arm passes, in **written** order.
type idxPair struct{ first, second idxCategory }

// idxPairLookupKinds is the fact `idxLookupKinds` deliberately defers: for an arm that passes *two*
// lookup categories, both of them.
//
// **The two tables disagree about the same kind, and the disagreement is the point.**
// `idxLookupKinds` records the category a *written* index takes, which for `table.init` is the
// **sugar** arm's — `TABLE_INIT idx { table_init (0l @@ …) ($2 c elem) }` (parser.mly:589), so one
// written index is an `elem`. This table records the **two-index** arm — `TABLE_INIT idx idx
// { table_init ($2 c table) ($3 c elem) }` (:587-588) — so `first` is `catTable` where the other
// table says `catElem`. Neither is wrong; they answer different questions, and a reader that
// consulted one for the other's question would resolve `table.init $t $e`'s first index in the
// element space: a legal image denoting a different instruction, invisible to the suite (§9 G-3).
//
// **Scoped to the space, not to the slice** (#33's widening rule): all eight arms are listed,
// including the five `fb` array forms this stratum cannot encode at all, so an arm arriving upstream
// lands as a failure rather than as an omission. TestIdxPairLookupKindsMatchTheReference is the
// authority, and it reads the *arm with the most* categories where the single-index control reads
// the arm with the fewest — the same extraction, the opposite selector.
//
// **`table.copy` and `memory.copy` are absent, and that is not an oversight.** Their arms read
// `idx_idx_opt`, which passes one lookup to *both* indices (`$1 c lookup, $2 c lookup`, :497), so
// the reference itself names one category for the pair and `idxLookupKinds` already holds it. A row
// here would be a second copy of that one fact.
var idxPairLookupKinds = map[keywordKind]idxPair{
	// The two whose second arm is sugar. `MEMORY_INIT idx idx` is
	// `memory_init ($2 c memory) ($3 c data)` (:607).
	"TABLE_INIT":  {catTable, catElem},
	"MEMORY_INIT": {catMemory, catData},

	// The five `fb` array arms (:70-78 of the production), all `type_` first. Listed because the
	// table is scoped to the space; refused at `encodableShapes` long before a category is consulted.
	"ARRAY_COPY":      {catType, catType},
	"ARRAY_NEW_ELEM":  {catType, catElem},
	"ARRAY_NEW_DATA":  {catType, catData},
	"ARRAY_INIT_ELEM": {catType, catElem},
	"ARRAY_INIT_DATA": {catType, catData},

	// `br_table`'s first label and the first member of its list take the same category, and
	// `idxList` resolves the rest — a row all the same, because the extractor sees two `c label`
	// in the arm and an unexplained omission is the unreachable-error pattern in a table's clothes.
	"BR_TABLE": {catLabel, catLabel},

	// **The two whose second space is not in the enum**, and they are `catFieldOfType` rather than
	// absent for #6's reason: `STRUCT_GET idx idx` is
	// `let x = $2 c type_ in $1 x ($3 c (field x.it)).it` (:622), whose second lookup is
	// `(field x.it)` — the field space *of the type the first index just named* (`field` at :162 is
	// `Lib.List32.nth c.types.fields x`), one space per struct type rather than one module-wide
	// space. So there is nothing for `idxSpaceFor` to return, and a row saying so is what keeps
	// the omission from reading as a gap.
	//
	// These two are why the pair control cannot reuse the single-index control's extractor. That one
	// matches `c <word>` against a ten-way alternation of the module-level space names; `(field
	// x.it)` is not a word in that alternation, so the alternation instrument finds **8** two-lookup
	// arms and a positional `$N c <expr>` instrument finds **10** — the discrepancy being exactly
	// these two kinds. A suspiciously clean agreement between two supposedly independent readings of
	// the same file is the tell grave #106 was filed for; here the two readings *disagree*, and the
	// disagreement is the finding. See TestIdxPairLookupKindsMatchTheReference, which uses the
	// positional reader for exactly this reason — and which now asserts the discrimination rather
	// than a count, because the 8 is also the pair floor and a floor cannot see a 2-of-10 loss.
	//
	// **This sentence said 9 until the extractor was written and printed 10.** A number typed ahead
	// of the instrument that measures it, in prose whose whole subject is two instruments disagreeing
	// by exactly the amount the wrong figure understated.
	"STRUCT_GET": {catType, catFieldOfType},
	"STRUCT_SET": {catType, catFieldOfType},
}

// pairCategories returns the categories a two-index arm's indices resolve against.
//
// The fallback is the copy arms' case, stated above: one category for both indices, held in
// `idxLookupKinds`. Written as a lookup with a fallback rather than as rows in `idxPairLookupKinds`
// so the pair table stays exactly the set the reference gives two lookups, which is what its drift
// control can check.
func pairCategories(k keywordKind) idxPair {
	if p, ok := idxPairLookupKinds[k]; ok {
		return p
	}
	cat := idxLookupKinds[k]
	return idxPair{cat, cat}
}

// idxSpaceFor returns the index space a category resolves against, or nil when this stratum cannot
// resolve it yet.
//
// A nil space is an *encode* refusal, not a parse failure: the recognizer already handles every one
// of these arms and must keep doing so. Refusing rather than defaulting is the point — a category
// resolved in the wrong space is the accept-direction defect idxCategory's comment describes, and
// "not yet" is a verdict this project prefers to a guess.
func (p *parser) idxSpaceFor(cat idxCategory) *space {
	switch cat {
	case catLocal:
		return &p.ctx.locals
	case catFunc:
		return &p.ctx.funcs
	case catMemory:
		return &p.ctx.memories
	case catGlobal:
		return &p.ctx.globals
	case catTable:
		return &p.ctx.tables
	case catType:
		return &p.ctx.types
	case catTag:
		return &p.ctx.tags
	case catData:
		return &p.ctx.datas
	case catElem:
		return &p.ctx.elems
	case catLabel, catNone, catFieldOfType:
		// A label is resolved by `labelIdx` against the lexical label stack, not by a space —
		// see labelTakingKinds. catNone reaches here only from a shape with no index.
		// catFieldOfType has no module-level space to be: see its constant.
		return nil
	}
	return nil
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
// **Emission happens here, after the immediates are read**, which is the only order available: an
// instruction's bytes are its opcode followed by its immediates, and the immediates are not known
// until they are parsed. So `immediates` accumulates into `p.imm` and this appends the finished
// instruction. The accumulator is reset per instruction rather than per call site, because a nested
// reader (a folded operand inside a memarg? not legal, but `select`'s result list is) must not
// inherit a half-built immediate list.
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
	saved := p.imm
	p.imm = nil
	defer func() { p.imm = saved }()
	if err := p.immediates(shape, t); err != nil {
		return true, err
	}
	if !p.retaining() {
		return true, nil
	}
	// **Before the two refusals below**, so the record is a property of the instruction rather than of
	// whether this stratum can encode it. Two of the four data-referencing mnemonics are refused a few
	// lines down (`array.new_data`'s `immIdxIdx` shape is not in `encodableShapes`), and recording
	// after those returns would make the flag mean "an *encodable* data reference" — a subtly
	// different question, and one whose answer changes as the frontier moves. Ordering only, since the
	// refusal aborts the encode either way; stated because a reader may otherwise move it.
	if dataRefKinds[t.Keyword] {
		p.ctx.sawDataRef = true
	}
	if !encodableShapes[shape] {
		// A shape whose immediates this file does not write — memarg's natural-alignment default,
		// the lane and vector forms, the reftype and heaptype arms. The *parse* was complete, so
		// this is an encode refusal, and it is checked before `opBytes` so the message names the
		// reason rather than the mnemonic's absence from a table it is in fact present in.
		return true, p.refuseUnencodable(t, "the "+t.Text+" instruction's immediates")
	}
	op, ok := opBytes(t.Text)
	if !ok {
		// A mnemonic with no unambiguous encoding: the three type-dependent ones, or one the
		// generated table does not carry. The *parse* succeeded, so this is an encode refusal and
		// not a syntax error — it reads as "this module is not encodable yet", which is what the
		// frontier message says for the fields that have no section.
		return true, errf(t, "cannot yet encode the %s instruction (#8)", t.Text)
	}
	p.emit(instr{op: op, imm: p.imm, patch: p.immPatch})
	p.immPatch = nil
	return true, nil
}

// immediates reads the immediate sequence for one shape.
//
// Each arm is the reference's production read literally, and the *order* of the reads is
// load-bearing where a shape has two of them: `align` runs after `offset`, because
// `idx_opt offset_opt align_opt` (parser.mly:596) is written that way and the lexer gives
// `offset=` and `align=` distinct token kinds — so a module writing them backwards is a syntax
// error the reference reports, not a set to be matched in any order.
func (p *parser) immediates(shape immShape, mnemonic Token) error {
	// The label-taking arms, whose first index resolves against the label space instead of being
	// deferred (#80). Handled before the switch rather than as extra shapes because a label is a
	// property of the *arm's lookup function*, not of its immediate shape: `BR` and `CALL` are both
	// `immIdx` and differ only in category, and `BR_TABLE` shares `immIdxIdxList` with nothing while
	// `BR_ON_CAST` shares `immIdxReftype2` with nothing — splitting the shape enum by category would
	// double three arms to encode one bit.
	if labelTakingKinds[mnemonic.Keyword] {
		switch shape {
		case immIdx: // BR, BR_IF, BR_ON_NULL — the label is the whole immediate
			return p.labelIdx()
		case immIdxIdxList:
			// **BR_TABLE reads its whole label sequence as one unit**, and this is the arm that
			// no longer shares the leading `labelIdx` read with the other two. Its immediates are
			// not the concatenation of what it reads: the encoding is `vec(labelidx) labelidx`,
			// so a count the parser does not have yet precedes the members and the last written
			// label is the *default* rather than a member. Retaining the first label on sight —
			// which is what a shared leading read forces — writes that label where the count
			// belongs. See brTable.
			return p.brTable()
		case immIdxReftype2: // BR_ON_CAST: `idx reftype reftype`, the label then two types (:567)
			if err := p.labelIdx(); err != nil {
				return err
			}
			if _, err := p.reftype(); err != nil {
				return err
			}
			_, err := p.reftype()
			return err
		default:
			// Unreachable while labelTakingKinds and the shape table agree, and a panic because
			// falling through to the main switch would read the label's immediates a second time.
			// TestLabelTakingArmsMatchTheReference pins the set; this pins the *pairing* — a new
			// label-taking arm upstream whose shape is not one of the three arrives here.
			//
			// Written as `default` rather than as a statement after the switch so that `exhaustive`
			// reads it: the linter's `default-signifies-exhaustive` makes a real fallback count as
			// handling the enum, and a bare panic below the switch is the same code the linter cannot
			// see. Three shapes named, not thirteen, because the other ten are the *main* switch's —
			// listing them here to satisfy a linter would claim this branch handles shapes no
			// label-taking mnemonic can have.
			panic("burroughs: label-taking mnemonic " + string(mnemonic.Keyword) +
				" has an unhandled immediate shape")
		}
	}
	switch shape {
	case immNone:
		return nil
	case immIdx:
		return p.idxRetained(mnemonic)
	case immIdxIdx:
		if err := p.idx(); err != nil {
			return err
		}
		return p.idx()
	case immIdxOpt:
		// `memory.size`/`memory.grow` write a **bare `idx`** (encode.ml `op 0x3f; idx x`, :601),
		// so the immediate is always present in the encoding even when the text omits it — the
		// empty arm means index 0, not "no immediate". An emitter that wrote nothing here would
		// produce a one-byte instruction the decoder reads as `memory.size` followed by whatever
		// came next.
		r, present, err := p.idxOpt()
		if err != nil {
			return err
		}
		if !present {
			// The reference's own default: `MEMORY_SIZE /* empty */ { fun c -> memory_size
			// (0l @@ …) }`. Index 0 written explicitly, because the encoding has no empty arm.
			p.appendImm(encodeLocalIdx(0))
			return nil
		}
		return p.retainIdx(mnemonic, r)
	case immIdxIdxOpt:
		// `idx_idx_opt` (:494) is empty-or-two, and the `memory.init`/`table.init` sugar arms
		// are one-or-two. Both are served by "read as many as are there", which is why the
		// second read is conditional rather than required after the first.
		first, present, err := p.idxOpt()
		if err != nil {
			return err
		}
		if !present {
			// `memory.copy` with neither index written: both default to 0, and **both are
			// written** — `MemoryCopy (x, y) → op 0xfc; u32 0x0al; idx x; idx y` (encode.ml:597)
			// has no arm that omits an index.
			p.appendImm(encodeLocalIdx(0))
			p.appendImm(encodeLocalIdx(0))
			return nil
		}
		second, present2, err := p.idxOpt()
		if err != nil {
			return err
		}
		return p.retainIdxPair(mnemonic, first, second, present2)
	case immMemarg:
		return p.memarg(mnemonic)
	case immLaneImms:
		// `lane_imms` (:661) is the memarg shape with a mandatory trailing laneidx, spelled out
		// upstream as five arms "to avoid spurious conflicts" rather than as a composition,
		// and the fifth is why this cannot *be* a composition: see laneImms.
		return p.laneImms(mnemonic)
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
		// **Unreachable, and named rather than deleted.** `BR_TABLE` is this shape's only
		// mnemonic and it is label-taking, so the branch above answers it and this arm cannot be
		// entered — the same standing that `immIdx`'s arm has for `BR`/`BR_IF`, which are also
		// label-taking and also served upstream. Deleting it would make the shape's presence in
		// `shapeOf` depend on the label branch's membership: a mnemonic dropped from
		// `labelTakingKinds` would then reach the closing panic rather than a reader. So the arm
		// stays as the shape's non-label reading — `br_table` takes one idx then `idx_list`
		// (:497), whose empty arm is why the loop exits on "no idx here" rather than erroring.
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
		return p.heaptypeRetained(mnemonic)
	case immIdxNat32:
		if err := p.idx(); err != nil {
			return err
		}
		return p.nat32()
	case immNum:
		return p.constImmRetained(mnemonic)
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
// **It returns the reference too, now that a caller encodes it.** The three-value signature is
// uglier than the two-value one and is the alternative to a "last index read" field on the parser,
// which would be a second channel for a value the function already has — the same objection the
// paragraph above makes to stashing the error.
func (p *parser) idxOpt() (idxRef, bool, error) {
	if !p.atIdx() {
		return idxRef{}, false, nil
	}
	r, err := p.idxValue()
	return r, true, err
}

// memarg parses `idx_opt offset_opt align_opt` (:596), the load/store immediates.
//
// The three are separately optional and *ordered*: `offset=` before `align=`, because that is
// how the production is written and the lexer hands back distinct kinds for the two. So
// `i32.load align=4 offset=0` is malformed, and this reports it by leaving `offset=` unconsumed
// for the caller's `unexpected token`.
//
// It parses into a memargImm and hands that to retainMemarg; the *encoding* is that function's,
// and the split is not stylistic — see it for why the write order cannot follow the read order.
//
// **The parsed value is not returned**, though the first draft returned it beside the error on the
// symmetry of every other retaining reader. Neither caller read it — `unparam` said so — and a
// returned value nobody reads is the unreachable-error shape wearing a result, which is the exact
// objection parseAlign's own doc makes about the value *it* used to discard. The day a caller needs
// the fields (the lane forms, whose retention wants a raw trailing byte) is the day the signature
// grows one, with a reader in the same commit.
func (p *parser) memarg(mnemonic Token) error {
	m := memargImm{align: -1}
	r, haveIdx, err := p.idxOpt()
	if err != nil {
		return err
	}
	m.idx, m.haveIdx = r, haveIdx
	if p.c.at(OffsetEqNat) {
		t := p.c.next()
		// `offset_` is `nat64` (:526) — 64 bits regardless of the memory's address type, which
		// is the width the *field* declares rather than the width the module uses. *When two
		// fields disagree about a value, the suite has handed you a bidirectional control.*
		v, ok := parseNat(offsetEqValue(t.Text), 64)
		if !ok {
			return errAt(t, "i64 constant out of range")
		}
		m.offset = v
	}
	if p.c.at(AlignEqNat) {
		t := p.c.next()
		align, pow2, isNat := parseAlign(t.Text)
		if !isNat {
			return errAt(t, "i64 constant out of range")
		}
		if !pow2 {
			return errAt(t, "alignment must be a power of two")
		}
		m.align = align
	}
	return p.retainMemarg(mnemonic, m)
}

// memargImm is one parsed memarg: the three separately-optional fields of `idx_opt offset_opt
// align_opt`, in the form the encoding needs them.
//
// `align` is **-1 when the text omitted `align=`**, which is the reference's `None` (align_opt,
// :534) rather than a sentinel invented here. Zero cannot serve: `align=1` is a legal written
// alignment whose exponent *is* 0, so a zero-means-absent encoding would give every `i32.load8_u`
// the same image whether or not its alignment was written — indistinguishable, in that one case,
// and wrong for `i64.load` where the default is 3.
type memargImm struct {
	idx     idxRef
	haveIdx bool
	offset  uint64
	align   int
}

// retainMemarg encodes a parsed memarg per `encode.ml:221`'s `memop`.
//
//	let memop x {align; offset; _} =
//	  let has_idx = x.it <> 0l in
//	  let flags = Int32.(logor (of_int align) (if has_idx then 0x40l else 0x00l)) in
//	  u32 flags; if has_idx then idx x; u64 offset
//
// **The write order is not the read order, and that is the whole reason this is a separate
// function.** The text writes the memory index *first* and the image writes the flags byte first,
// so the index cannot be appended as it is parsed the way every other retaining reader does it —
// `idxRetained` appends at the cursor, and doing that here would put the index ahead of the flags
// and encode a different instruction. So the memarg is parsed into a value and written after.
//
// **`has_idx` is a test on the value, not on the presence**, which is the subtle half. `idx_opt`
// (:492) returns `0l` for the empty production, so an *omitted* memory index and a written `0`
// produce the identical AST and the identical image: no 0x40 bit, no index field. `(memory 0)
// (i32.load 0)` and `(memory 0) (i32.load)` are the same bytes. Reading `has_idx` as "the text
// wrote one" would set 0x40 and emit an index for the explicit zero — a legal image whose flags
// byte says a memory index follows, decoding the offset LEB as that index. No `assert_malformed`
// can see it; `memory-multi.wast`'s round trip can.
//
// A symbolic index resolves against the memory space, which does not permit forward references
// (memories are declared before the code section's bodies are read), so this resolves at the
// cursor rather than deferring like `catFunc`.
//
// **That parenthetical is a fact about the binary and it was read as a fact about the text.**
// The section order does constrain the *image*; `module_fields` admits any field order in the
// *source*, so `(module (func (data.drop $s)) (data $s …))` is a valid module this parser rejects
// while the reverse order it accepts — resolution at the cursor is one pass where the reference has
// two, and `catFunc`'s deferral is that second pass built for exactly one category. Every
// non-catFunc space is affected, data segments being the only one with an encodable opcode today.
// Grave #130, where the scope table and the both-orders control live.
func (p *parser) retainMemarg(mnemonic Token, m memargImm) error {
	if !p.retaining() {
		return nil
	}
	align := m.align
	if align < 0 {
		// `align_opt`'s `None` becomes the mnemonic's natural alignment, which is the reference's
		// own `opt a N` default read out of lexer.mll (memarg.go, generated). A mnemonic absent
		// from that table takes no memarg at all, so its presence here would mean the shape table
		// and the alignment table disagree — refused rather than defaulted to zero, since a zero
		// exponent is a legal alignment and would encode silently.
		nat, ok := naturalAlign[mnemonic.Text]
		if !ok {
			return errf(mnemonic, "cannot yet encode %s: no natural alignment (#8)", mnemonic.Text)
		}
		align = int(nat)
	}

	idx := uint32(0)
	if m.haveIdx {
		resolved, err := p.ctx.memories.resolveSpaceIdx(m.idx)
		if err != nil {
			return err
		}
		idx = resolved
	}

	// The value test, not the presence test. See the doc above.
	hasIdx := idx != 0

	flags := uint32(align)
	if hasIdx {
		flags |= 0x40
	}
	var w writer
	w.u32(flags)
	if hasIdx {
		w.u32(idx)
	}
	w.u64(m.offset)
	p.appendImm(w.b)
	return nil
}

// laneImms parses `lane_imms` (parser.mly:661-673) — and it is **not** `memarg laneidx`, which is
// the whole content of grave #76.
//
// The reference multiplies the production out into five arms, with a comment saying why: *"Need to
// multiply out options and indices to avoid spurious conflicts"*. Written as a composition, the
// first NAT is ambiguous — `v128.load8_lane 0` has one NAT and it is the **lane index**, while
// `v128.load8_lane 1 0` has two and the first is the *memory* index. `idx_opt` is greedy, so
// composing `memarg` with `laneidx` eats the lone lane index as a memory index and then finds
// nothing where the mandatory laneidx should be:
//
//	v128.load8_lane 0 (i32.const 0) (v128.const i32x4 0 0 0 0)   →  unexpected token
//
// Ten must-succeed vectors, one per `simd_{load,store}{8,16,32,64}_lane.wast` plus
// `simd_memory-multi.wast` — accept-direction, so **no `assert_malformed` can see this**, and it
// was invisible until #69 raised the accept oracle from 7 modules to 2130.
//
// The five arms, and what distinguishes them here:
//
//	NAT offset_opt align_opt laneidx  :663   two NATs — the first is a memory index
//	VAR offset_opt align_opt laneidx  :666   a VAR can only be a memory index
//	offset_ align_opt laneidx         :669   no leading idx at all
//	align laneidx                     :671   ditto
//	laneidx                           :673   one NAT — the lane index (the arm that was eaten)
//
// So the decision is *whether a leading NAT is followed by something that can continue the
// memarg*, and it needs exactly two tokens of lookahead — the bound `peek2` already documents. A
// leading NAT is a memory index iff another NAT, `offset=` or `align=` follows it; a VAR is
// always one, since `laneidx` is `nat8` and admits no symbolic spelling. Everything after that
// first decision is the memarg reader unchanged, which is why this delegates rather than
// reimplements: the `offset=`-before-`align=` ordering and the two width checks stay in one place.
//
// **Scoped to all five arms, not to the spellings the corpus contains** (#76's definition of
// done). A fix that merely stopped reading a memory index would pass the same ten vectors and
// break arm 1, and only `simd_memory-multi.wast` writes a two-NAT form — so the control pins each
// arm by hand and the falsification is the two-NAT case, not the bare one.
// **It stays unencodable, and the reason is a shape this file's retention cannot express.** The
// lane forms are `memop x mo; u8 i` (encode.ml:387) — the memarg's bytes, then a *raw* byte for the
// lane index, not a LEB. `laneidx` reads the lane through `parseNat` and discards it, so retaining
// this arm means a second immediate writer, and arm 5 additionally has no memarg to write at all.
// The refusal is `encodableShapes`', which is where the frontier is one legible list; passing the
// mnemonic through keeps the natural-alignment lookup honest for the day the shape is added.
func (p *parser) laneImms(mnemonic Token) error {
	if p.c.at(NatTok) && !p.natContinuesMemarg() {
		// Arm 5: the lone NAT is the laneidx. Nothing for memarg to read, and calling it anyway
		// would consume the token as `idx_opt`.
		return p.laneidx()
	}
	if err := p.memarg(mnemonic); err != nil {
		return err
	}
	return p.laneidx()
}

// natContinuesMemarg reports whether the NAT under the cursor is a *memory index* rather than a
// lane index, by looking at what follows it.
//
// Split out of laneImms and named for the question rather than inlined as a `||` chain, because
// the three kinds here are a claim about `lane_imms`' first four arms: a memory index is followed
// by the laneidx (arm 1's second NAT), or by `offset=`/`align=` before it. Anything else — RPAR,
// a folded `(`, EOF — means the NAT was the last immediate, so it was the lane index.
//
// Over-answering here rejects a valid module and under-answering accepts an invalid one, and
// **the corpus only has vectors for the second**, so the first is stated as the risk and pinned
// synthetically.
func (p *parser) natContinuesMemarg() bool {
	switch p.c.peek2().Kind {
	case NatTok, OffsetEqNat, AlignEqNat:
		return true
	default:
		return false
	}
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
