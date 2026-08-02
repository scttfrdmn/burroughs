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
