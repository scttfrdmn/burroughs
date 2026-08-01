package text

// The keyword kinds the module grammar matches on.
//
// keywords.go is generated and holds the vocabulary's *authority*: 589 keywords mapping to 173
// kinds, machine-derived from lexer.mll. These constants are not a second declaration of that
// set — they are the subset the parser mentions, given names so a typo is a compile error
// instead of a silently-never-matching string comparison. `p.c.atKeyword("FNUC")` compiles and
// is always false, which is the unreachable-branch shape wearing a string literal.
//
// What keeps them honest is TestEveryKindConstantIsInTheTable, which asserts every constant
// here appears as a kind in the generated table. That is the direction that can rot: a kind
// renamed upstream, or misspelled here, would leave a production matching nothing — and under
// #62's reject-only surface a production that matches nothing *still returns an error*, just
// the wrong one, which is exactly the class no expected-string check catches. Scoped to the
// space by reflecting over the table rather than by listing what it should contain.
const (
	// Heap types (parser.mly:361-374).
	kwAny      keywordKind = "ANY"
	kwNone     keywordKind = "NONE"
	kwEq       keywordKind = "EQ"
	kwI31      keywordKind = "I31"
	kwStruct   keywordKind = "STRUCT"
	kwArray    keywordKind = "ARRAY"
	kwFunc     keywordKind = "FUNC"
	kwNofunc   keywordKind = "NOFUNC"
	kwExn      keywordKind = "EXN"
	kwNoexn    keywordKind = "NOEXN"
	kwExtern   keywordKind = "EXTERN"
	kwNoextern keywordKind = "NOEXTERN"

	// Reference type abbreviations (parser.mly:377-389).
	kwAnyref        keywordKind = "ANYREF"
	kwNullref       keywordKind = "NULLREF"
	kwEqref         keywordKind = "EQREF"
	kwI31ref        keywordKind = "I31REF"
	kwStructref     keywordKind = "STRUCTREF"
	kwArrayref      keywordKind = "ARRAYREF"
	kwFuncref       keywordKind = "FUNCREF"
	kwNullfuncref   keywordKind = "NULLFUNCREF"
	kwExnref        keywordKind = "EXNREF"
	kwNullexnref    keywordKind = "NULLEXNREF"
	kwExternref     keywordKind = "EXTERNREF"
	kwNullexternref keywordKind = "NULLEXTERNREF"

	// Value and storage types. NUMTYPE and VECTYPE and PACKTYPE are *classes* in the
	// reference's lexer — `i32`, `i64`, `f32`, `f64` all lex to NUMTYPE — which is why
	// addrtype has to read the token's text to tell i32 from f32.
	kwNumtype  keywordKind = "NUMTYPE"
	kwVectype  keywordKind = "VECTYPE"
	kwPacktype keywordKind = "PACKTYPE"

	// Type structure (parser.mly:400-458).
	kwNull   keywordKind = "NULL"
	kwRef    keywordKind = "REF"
	kwMut    keywordKind = "MUT"
	kwField  keywordKind = "FIELD"
	kwParam  keywordKind = "PARAM"
	kwResult keywordKind = "RESULT"
	kwSub    keywordKind = "SUB"
	kwFinal  keywordKind = "FINAL"
	kwRec    keywordKind = "REC"
	kwType   keywordKind = "TYPE"

	// Module fields (parser.mly:959-1382).
	kwModule  keywordKind = "MODULE"
	kwImport  keywordKind = "IMPORT"
	kwExport  keywordKind = "EXPORT"
	kwGlobal  keywordKind = "GLOBAL"
	kwMemory  keywordKind = "MEMORY"
	kwTable   keywordKind = "TABLE"
	kwElem    keywordKind = "ELEM"
	kwData    keywordKind = "DATA"
	kwStart   keywordKind = "START"
	kwTag     keywordKind = "TAG"
	kwLocal   keywordKind = "LOCAL"
	kwOffset  keywordKind = "OFFSET"
	kwItem    keywordKind = "ITEM"
	kwDeclare keywordKind = "DECLARE"
)

// parserKinds is every constant above, for the table-membership control.
//
// A slice rather than the test enumerating them, so adding a constant without adding it here is
// the only way to escape the check — and `deadcode` plus the count floor in
// TestEveryKindConstantIsInTheTable are what make that omission visible. The alternative,
// reflecting over package constants, Go does not offer.
var parserKinds = []keywordKind{
	kwAny, kwNone, kwEq, kwI31, kwStruct, kwArray, kwFunc, kwNofunc, kwExn, kwNoexn,
	kwExtern, kwNoextern,
	kwAnyref, kwNullref, kwEqref, kwI31ref, kwStructref, kwArrayref, kwFuncref,
	kwNullfuncref, kwExnref, kwNullexnref, kwExternref, kwNullexternref,
	kwNumtype, kwVectype, kwPacktype,
	kwNull, kwRef, kwMut, kwField, kwParam, kwResult, kwSub, kwFinal, kwRec, kwType,
	kwModule, kwImport, kwExport, kwGlobal, kwMemory, kwTable, kwElem, kwData, kwStart,
	kwTag, kwLocal, kwOffset, kwItem, kwDeclare,
}
