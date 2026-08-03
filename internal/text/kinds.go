package text

import "strings"

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

	// VECSHAPE is the lane layout `v128.const` and `i8x16.shuffle` take (lexer.mll:152-157):
	// another class, six lexemes, and the shape is what decides the lane count.
	kwVecshape keywordKind = "VECSHAPE"

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

	// The block family (parser.mly:726-738). These are `blockinstr`'s leaders and terminators,
	// and they are here rather than in plaininstrShapes because `blockinstr` is a separate
	// production from `plaininstr` — the mnemonic does not determine an immediate shape, it
	// opens a nested instruction sequence with its own label scope.
	kwBlock    keywordKind = "BLOCK"
	kwLoop     keywordKind = "LOOP"
	kwIf       keywordKind = "IF"
	kwElse     keywordKind = "ELSE"
	kwEnd      keywordKind = "END"
	kwTryTable keywordKind = "TRY_TABLE"
	kwCatch    keywordKind = "CATCH"
	kwCatchRef keywordKind = "CATCH_REF"
	kwCatchAll keywordKind = "CATCH_ALL"

	// `expr1`'s non-plaininstr leaders (parser.mly:815-823) that are not already above.
	// BLOCK/LOOP/IF/TRY_TABLE are shared with `blockinstr` and declared with the block family;
	// these three are folded-only, and they are named here rather than at their use site because
	// a keyword kind mistyped as a string literal is a branch that is silently never taken.
	kwSelect             keywordKind = "SELECT"
	kwCallIndirect       keywordKind = "CALL_INDIRECT"
	kwReturnCallIndirect keywordKind = "RETURN_CALL_INDIRECT"

	// THEN is the folded `if`'s mandatory arm (`if_`, :893/:896). ELSE is shared with the flat
	// family above — the folded form reuses the keyword, not the production.
	kwThen keywordKind = "THEN"

	// CATCH_ALL_REF is the fourth handler arm (:805). Named for completeness of the
	// `handler_block_body` production rather than because a vector reaches it — a handler set
	// missing one arm rejects a legal module, which is the accept-direction class no
	// assert_malformed can catch.
	kwCatchAllRef keywordKind = "CATCH_ALL_REF"
)

// heapWat renders an *absolute heap type's* kind as its wat spelling.
//
// **Scoped to `absoluteHeaptypes` and nothing wider, because the general version is false.** A
// keyword kind is the lexer's token *class*, not a spelling: `LOCAL_GET` is the class for
// `local.get` (a dot, not an underscore), `NUMTYPE` is the class all four number types share, and
// `VEC_BINARY` names no literal at all. Measured over the generated table: lower-casing a kind
// yields that same kind's keyword for **96 of 173**, and for one it yields a *different* kind —
// `BINARY` lower-cases to `binary`, which lexes to `BIN`. A wrong spelling, not a missing one,
// which is the shape that would make a diagnostic quote something the user never wrote (grave #36).
//
// The twelve heap types are the subset where the derivation holds, and they need it because a
// resolved value type retains no token — resolveVal keeps `abs`, having consumed the keyword. Every
// other diagnostic in this package quotes `Token.Text` instead and should keep doing so: bindAbs's
// comment gives the reason, and it is the reference's own practice.
//
// The domain is the parser's existing list rather than a fresh enumeration, so a thirteenth heap
// type is covered by the control the day it is added — derive the domain, never enumerate it.
// TestEveryAbsoluteHeaptypeRoundTripsThroughTheKeywordTable is what holds the premise.
func heapWat(k keywordKind) string { return strings.ToLower(string(k)) }

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
	kwNumtype, kwVectype, kwPacktype, kwVecshape,
	kwNull, kwRef, kwMut, kwField, kwParam, kwResult, kwSub, kwFinal, kwRec, kwType,
	kwModule, kwImport, kwExport, kwGlobal, kwMemory, kwTable, kwElem, kwData, kwStart,
	kwTag, kwLocal, kwOffset, kwItem, kwDeclare,
	kwBlock, kwLoop, kwIf, kwElse, kwEnd, kwTryTable,
	kwCatch, kwCatchRef, kwCatchAll, kwCatchAllRef,
	kwSelect, kwCallIndirect, kwReturnCallIndirect, kwThen,
}
