package text

import (
	"bytes"
	"fmt"
	"slices"
	"strconv"
)

// The type index space's *content*, and the deferred phase that reads it.
//
// Everything else in this package is positional: a production says whether the tokens in front
// of it are well-formed and returns an error. Two of the reference's helpers cannot be written
// that way, and they are the last two grammar-level checks the parser owed:
//
//	inline_functype_explicit c x ft   (parser.mly:237-247)  "inline function type does not match
//	                                                         explicit type", "unknown type <n>",
//	                                                         "non-function type <n>"
//	inline_functype c ft loc          (parser.mly:222-235)  creates or reuses an implicit type
//
// The first **compares** an inline `(param …)/(result …)` signature against the functype the
// explicit `(type n)` names, structurally. So the parser must retain each type definition's
// functype *content*, and the valtype readers must yield values rather than only errors. That is
// within decision 0011: 0011 governs the parser's *surface* — error-only, no module value out —
// and `context` already exists as internal state for exactly the checks the reference puts inside
// its grammar. Nothing here escapes the package.
//
// # Why the phase, and why it cannot be one pass
//
// **The reference resolves type names after every name is bound, and the corpus depends on it.**
// `module_` applies its field list to *two* unit arguments (parser.mly:1389-1392), and each arm of
// `module_fields1` is `fun c -> … fun () -> … fun () -> …`, so there are three stages:
//
//	stage 0  at reduction, as `$1 c` runs left to right — every *name* is bound
//	         (`bind_type`/`bind_func`/…, reached through `type_def`'s and `func`'s `fun c ->`)
//	stage 1  first `()` — the `type_` arms run their bodies: `define_type`/`define_deftype`,
//	         so the whole *explicit* type table exists, in source order
//	stage 2  second `()` — the `func`/`tag`/… arms run, and this is where the two helpers above
//	         are called
//
// `imports.wast:62-64` is the corpus proving stage 0 has to precede stage 2:
//
//	(import "spectest" "print_i32" (func (type $forward)))
//	(func (import "spectest" "print_i32") (type $forward))
//	(type $forward (func (param i32)))
//
// A resolver that looked `$forward` up where it is *used* would reject that module, and it is a
// valid one — the accept-direction failure §9 G-3 names, invisible to every `assert_malformed`.
// So resolution is deferred here as it is deferred there: the parse records the operations and
// `runDeferred` executes them once the field list is complete.
//
// **Recording in parse order reproduces stage-2 order**, which is the load-bearing claim and not
// an obvious one. Two facts make it true. Across fields, `fun () -> let funcs = ff () in let m =
// mf () in …` runs the head field's own work before descending into the tail, so fields run in
// source order. Within a `func`, `func_fields`'s first arm (:963-969) evaluates
// `inline_functype_explicit c' ($1 c') (fst $2 c')` — the signature — before `snd $2 c'`, the
// body, so a function's own typeuse precedes the blocks nested inside it. Both are source order,
// so appending to one list as the parser walks the text is the same sequence.
//
// It matters because `inline_functype` *appends* to the type space, so the index an implicit type
// gets is a function of that order, and `unknown type <n>` is a function of the table's length.
//
// # One thing this deliberately does not do
//
// `inline_functype_explicit`'s deferred branch (:240-245) also binds the named type's params into
// the function's local space, so `(func (type $sig) (local.get 0))` has locals it never wrote. The
// local space is per-function and `enter_func` resets it (:134), so honouring that here would mean
// carrying a live local space per pending operation. It is not done, and it is tracked in #77.
//
// **The "zero board effect either way" this paragraph claimed is now false, and the correction is
// the reason to read #77 as live rather than cosmetic.** The claim rested on nothing resolving a
// local — true while the parser was only a recognizer, and untrue the moment the code section began
// *writing* local indices. A short local space makes `(func (type $sig) (local $var i32)
// (local.get $var))` encode `$var` as slot 0 where `$sig`'s param owns 0: a well-formed image
// denoting a different function. Found by the wabt corpus at one byte, invisible to all 4162
// vectors. The frontier now refuses that case in `retainIdx` (code.go) rather than emitting it, so
// the consequence is a *declined* module instead of a wrong one — and #77 is what makes the
// question answerable rather than merely refusable.
//
// Kept as a correction with its body intact rather than rewritten, because a scope note that went
// stale by the code around it growing a consumer is the drifted-citation defect's own shape, and the
// record of what was believed is the part worth keeping.

// valType is one wat value type, carrying as much as a structural comparison needs
// (parser.mly:391-394).
//
// A number or vector type is its token spelling: the reference's lexer collapses `i32`/`i64`/
// `f32`/`f64` to one NUMTYPE class and keeps the payload, and `NumT $1` is that payload — so the
// spelling is the value, not a stand-in for it. `num == ""` means this is a reference type, and
// then null and heap carry it.
type valType struct {
	num  string // "i32", "f64", "v128", … ; "" for a reference type
	null bool   // reftype nullability — `(ref null $t)` and every abbreviation
	heap heapRef
}

// heapRef is a heap type (parser.mly:361-374): one of twelve absolute forms, or a type index.
//
// The index is held as its **token**, unresolved, because a `$name` here may forward-reference a
// type defined later in the field list — the same reason the whole phase is deferred. abs == ""
// is what says "read tok instead".
type heapRef struct {
	abs keywordKind
	tok Token
}

// funcType is a `(param …)*(result …)*` signature, unresolved (parser.mly:430-444).
type funcType struct {
	params  []valType
	results []valType
}

// isEmpty reports whether this is the reference's `([], [])` — the case
// `inline_functype_explicit` **defers** rather than compares (:238).
//
// It is a distinct question from "the comparison would succeed against an empty type", and
// conflating them is a real trap: `(func (type 2))` in a module with two types must *not* be
// `unknown type` from the parser, because the lookup never happens. `func.wast:442` asserts that
// spelling is `assert_invalid` — the validator's — while `func.wast:454`'s `(func (type 2) (param
// i32))` is `assert_malformed` on the same index, because the inline signature forces the lookup.
// One vector each, in opposite directions, on the same module: the suite pins the deferral
// directly.
func (ft funcType) isEmpty() bool { return len(ft.params) == 0 && len(ft.results) == 0 }

// inlineBlockType reports whether this signature is one a blocktype can spell *without* a type
// index — the reference's `([], [])` and `([], [t])` arms (parser.mly:746-751).
//
// **Named because two different mechanisms consult it and must agree.** The interner uses it to
// decide whether to append to the type space (declareBlockImplicit) and the encoder uses it to
// decide whether the opener's immediate is a `0x40`/valtype byte or an `s33` index — and an
// encoder that wrote an index where nothing was interned would name a slot the module never
// declared. One predicate, so the disagreement is unrepresentable rather than merely unlikely.
//
// Distinct from isEmpty, which is the `([], [])` case alone: a `(result i32)` block is *not* empty
// and still needs no type. Conflating them would intern a type for the commonest spelling in the
// corpus and shift every later implicit index.
func (ft funcType) inlineBlockType() bool {
	return len(ft.params) == 0 && len(ft.results) <= 1
}

// compKind distinguishes comptype's three forms (parser.mly:446-449), mirroring
// `binary.CompKind`'s already-merged three values — the decoder-side authority for this exact
// grammar production (`internal/binary/module.go`).
//
// **Replaces the old `isFunc bool`, as of decision 0021.** A bool could tell a func apart from
// "not a func", which was everything `func_type`'s `non-function type` fallthrough needed; a
// struct and an array are now two distinct populations with their own retained content
// (`compType.fields`), so the type needs three states rather than two — the same reason
// `binary.CompKind` is a byte enum and not a bool over there.
type compKind byte

const (
	compFunc compKind = iota
	compStruct
	compArray
)

// storageType is a fieldtype's storage, unresolved (parser.mly:404-406): a value type, or one of
// the two packed widths (decision 0021).
//
// Mirrors `binary.StorageType`'s already-merged shape (`internal/binary/module.go`) at this
// package's own unresolved stratum — `val` is meaningful when `!packed`, matching `valType`'s own
// "may forward-reference a type index" property, so a struct or array field's value-typed storage
// resolves in the same deferred phase a functype's param does.
type storageType struct {
	val    valType // meaningful when !packed
	packed bool
	width  byte // 8 or 16, meaningful only when packed
}

// fieldType is one struct or array field, unresolved: its storage and whether it may be written
// after allocation (parser.mly:408-410, decision 0021).
//
// **No name field.** 0021 excludes a field's identifier from retention by the wire-form
// authority: `fieldtype`'s production carries no identifier (decode.ml:243-246), so there is
// nothing here for the encoder to write regardless of what the text bound one to — `LocalGroup`'s
// precedent (0016) applied a second time, per the ADR's own citation.
type fieldType struct {
	storage storageType
	mut     bool
}

// compType is a composite type (parser.mly:446-449).
//
// **`fields` carries a struct's or array's content, as of decision 0021** — one entry per
// declared field for a struct, in declaration order, or exactly one for an array, mirroring
// `binary.CompType.Fields`'s own comment on the arity (`comptype`'s two different productions,
// decode.ml:250-259: a struct's `vec(fieldtype)` versus an array's bare `fieldtype`). Meaningful
// only when `kind` is `compStruct` or `compArray`; `ft` is meaningful only when `kind` is
// `compFunc`. Two fields rather than a tagged union with one live field, matching `binary.CompType`'s
// own shape choice and its stated reason: a switch on `kind` reads no worse than an interface
// dispatch here, and the population (module type definitions, not a hot per-instruction path) is
// nowhere near where 0002's boxing argument bites.
//
// **`fieldNames` is a fourth field, added for #188 and not a reopening of 0021's rule about
// `FieldType`.** 0021 excludes a field's identifier from `fieldType` itself, by the wire-form
// authority (`fieldtype`'s production carries no identifier) — that stays true, and this map is a
// different fact: the *type-level* binding from a struct's field names to their positions, the
// reference's `field x (c) y = lookup "field " (Lib.List32.nth c.types.fields x) y`
// (parser.mly:162-163). `structtype` built this binding all along, locally, to enforce the
// duplicate-field check and size the anonymous-field loop, and discarded it on return; this field
// is where it survives, for `struct.get $vec $y`'s benefit — see `resolveFieldIdx` (code.go), its
// one consumer. `nil` for `compFunc` and `compArray`, which bind no field names (`arraytype` has
// no `bindidx` arm in the grammar), and that is not a special case anywhere that reads it: a nil
// map lookup reports "not found" exactly as an empty one would, matching the reference's own
// per-type field space being empty for those two kinds rather than absent.
//
// **`final` and `supertypes` are a fifth and sixth field, for 0019's own named gap** —
// `subtype`'s own two facts, previously read and discarded (`idxList`, the old doc comment on
// this function's caller). `final` is true for a bare comptype or an explicit `(sub final
// idx* ct)`, matching the reference's default (`SubT (Final, [], ct)` for the no-wrapper form,
// `subtype` :451-458) — the two are structurally identical for every consumer this codebase has,
// so there is nothing gained by distinguishing "no wrapper" from "wrapper, spelled final" and
// this field does not try to. It is false only for an explicit `(sub idx* ct)` with no `final`
// keyword. `supertypes` is unresolved, `idxRef`'s own convention (may name a type bound earlier
// in the same module, per `check_subtype_sub`'s forward-reference rule, valid.ml:169-171) —
// resolved at `runDeferred`'s stage 2, the same stage every other forward-referencing index in
// this table resolves at.
type compType struct {
	kind       compKind
	ft         funcType
	fields     []fieldType
	fieldNames map[string]uint32
	final      bool
	supertypes []idxRef
}

// recExtent is one `rectype` field's extent in the explicit type space: the index of its first
// member and how many members it has.
//
// Length 1 for a bare `(type …)` — the grammar's non-`rec` arm is `RecT [st]` (decode.ml:276), a
// singleton group and not an absence of one — and length 0 for `(rec)`, which is legal and defines
// nothing (`type-rec.wast:10`).
type recExtent struct {
	start, length uint32
}

// limits is a `limits` (parser.mly:466-468): a 64-bit minimum and an optional maximum.
//
// Both at 64 bits because the reference's arms are `nat64`, and `hasMax` rather than a `*uint64`
// because that is the shape `binary.Limits` has on the other side — a difference in representation
// between the two sides of one round trip is a place for a nil to mean zero.
type limits struct {
	min    uint64
	max    uint64
	hasMax bool
}

// memType is a `memorytype` (parser.mly:463-464).
//
// `addr64` is the addrtype, held as a bool rather than a keyword because the binary format holds it
// as **one flag bit** (encode.ml:187) and the text grammar admits exactly two spellings — `addrtype`
// already rejected everything else as `malformed address type`. A keyword field would be a wider
// domain than either grammar has, inviting a third case that cannot occur.
type memType struct {
	addr64 bool
	lim    limits
}

// tabType is a `tabletype` (parser.mly:460-461), element type **unresolved**.
//
// The element type is a `valType`, the same unresolved form a functype's params hold, and for the
// same reason: a `(ref null $t)` in a table may forward-reference. `defineTable` resolves it in the
// deferred phase.
type tabType struct {
	addr64 bool
	lim    limits
	elem   valType
}

// globalType is a `globaltype` (parser.mly:400-402), value type **unresolved**.
//
// The mutability is a bool because the binary form is one byte with two values (encode.ml:104-106,
// `Cons -> byte 0 | Var -> byte 1`) and the text grammar has exactly the two arms — the same
// argument `memType.addr64` records for the address type. A `keywordKind` here would be a wider
// domain than either grammar has.
type globalType struct {
	val valType
	mut bool
}

// resolvedGlobal is a globalType whose value type has been resolved.
type resolvedGlobal struct {
	val resolvedVal
	mut bool
}

// textGlobal is one *defined* global as the parse read it: its type and its initializer (#8).
//
// **Two fields because `global` is two things**: `let Global (gt, c) = g.it in globaltype gt; const c`
// (encode.ml:991-993). The type is unresolved for `globalType`'s reason — a `(ref null $t)` may name a
// type defined later — and the initializer is an `instrSink` rather than bytes because an
// instruction's *position* in the expression is the one thing that cannot be recovered in stage 2
// (`instr`'s comment). Each instruction's own symbolic index still defers individually, through
// `instr.patch`.
//
// Distinct from `resolvedGlobal` above, which is the *type* pair the import descriptor holds: an
// imported global has a globaltype and no initializer, and merging them would put a field in the
// import's descriptor that is meaningless for every import — the split `textData`/`resolvedData` and
// `tabType`/`resolvedTable` both make, for the same reason.
type textGlobal struct {
	typ  globalType
	init instrSink
}

// resolvedGlobalDef is a defined global after stage 2: the type resolved and the initializer encoded,
// so the writer cannot fail.
//
// The pairing is the reference's — `globaltype gt; const c` — and the fields are in that order
// because section 6's bytes are.
type resolvedGlobalDef struct {
	typ resolvedGlobal
	// init is the const expression's bytes **including** its `0x0b` terminator, exactly as
	// `resolvedData.offset` holds an offset's: `const c` is `list instr c.it; end_ ()`
	// (encode.ml:912-913), and `constexpr` has no `end` token in the text.
	init []byte
}

// importDesc is one import's descriptor: the kind, and whichever of the five payloads that kind
// carries. What `externtype` retained, after the deferred phase resolved it.
//
// **A tagged union spelled as a struct with one live field per kind, rather than five parallel
// slices or an interface.** The reason is the *vector*: the import section is `vec(import)` in
// source order (encode.ml:938-943), so every import must sit in one ordered list regardless of
// kind — five slices would need a sixth list to record the interleaving, which is the same fact
// stored twice. An interface would put a method on each payload, and the payloads are three
// existing structs plus two indices; the switch that would dispatch it lives in one place
// (`encodeImports`) and reads better as a switch than as five one-line methods.
//
// `typeIdx` covers **both** the func and tag kinds. That is the reference's own shape rather than a
// merge: `ExternFuncT ut` and `ExternTagT (TagT ut)` both carry a typeuse (parser.mly:1229/:1232),
// and the binary forms differ only in the tag's extra attribute byte (`u32 0x00l`, encode.ml:191).
// Two fields would be two names for one number, and the kind already discriminates.
type importDesc struct {
	kind    importKind
	typeIdx uint32        // func and tag: the resolved type index
	table   resolvedTable // table
	mem     memType       // memory
	global  resolvedGlobal
}

// textImport is one retained import: two names and a descriptor, in source order.
//
// The names are `string` rather than `[]byte` — `decodedName`'s comment has the copy argument, which
// is `decodeImport`'s on the other side of the round trip.
type textImport struct {
	module string
	name   string
	desc   importDesc
}

// textExport is one retained export: a name, which of the five spaces it names, and the resolved
// index into that space.
//
// `kind` is `importKind` rather than a new export-specific type, and this is a *derivation* rather
// than a convenience. The reference's `externidx` (encode.ml:1001-1007) and `externtype`
// (:202-208) assign the same five kind bytes in the same order — func 0x00, table 0x01, memory
// 0x02, global 0x03, tag 0x04 — so an export's kind byte is the identical fact an import's
// descriptor already carries, and `externKindByte` serves both. The naming is now off by one
// grammar (an `importKind` in an export), which is the price of not having two enums that must
// agree; TestExternKindByteAgreesForBothSections is what holds the agreement, so the shared type is
// checked rather than assumed. Grave #105's lesson pointed at a type instead of a regexp.
type textExport struct {
	name string
	kind importKind
	idx  uint32
}

// textData is one retained data segment: its mode, its memory index, its offset expression and its
// payload — section 11's content (#8).
//
// **`passive` is a field rather than a nil-offset test**, because an *active* segment may legally have
// an empty offset expression. `offset` is `LPAR OFFSET constexpr RPAR` and `constexpr` is `instr_list`
// (parser.mly:1091, :950), so `(data (offset) "")` parses with zero instructions — and reading
// nil-ness as "passive" would encode that as `0x01`, a passive segment, where the reference writes
// `0x00` with an empty const expr. Same module shape, different segment mode, decoding clean.
//
// **`mem` is unresolved, and that is what makes the arm discriminator honest.** `encode.ml`'s `data`
// splits on the *resolved* index — `Active ({it = 0l; _}, c)` takes the two-byte `0x00` form and
// `Active (x, c)` the three-byte `0x02` form (:1096-1101) — so the question is "is this index zero",
// never "did the text write a `(memory …)`". `(data (memory 0) (offset (i32.const 0)) "")` and
// `(data (offset (i32.const 0)) "")` are therefore the *same* encoding, and a `haveMem`-style
// discriminator (which this type's first draft carried) would emit `0x02 00` for the first: a legal
// image, a byte longer, denoting the same module — and a gratuitous divergence from every other
// producer, wabt included, which #67's corpus compares against. A symbolic `(memory $m)` naming
// memory 0 is the case that makes the distinction more than pedantic: it can only be answered after
// resolution.
type textData struct {
	passive bool
	// mem is the memory index, meaningless when passive. The zero value is the sugar arm's default,
	// which is exactly the reference's `Active (0l @@ $sloc, …)` (parser.mly:1105) — an `idxRef` with
	// `isVar` false and `idx` 0 resolves to 0, so the default needs no separate flag.
	mem    idxRef
	offset instrSink
	bytes  []byte
}

// resolvedData is a data segment after stage 2: the memory index resolved and the offset's
// instructions encoded, so the writer cannot fail.
//
// Distinct from `textData` for `resolvedTable`'s reason and `encodedFunc`'s: the offset can hold a
// `global.get $g` naming a global defined later, so its bytes are not knowable at the cursor, and a
// writer that resolved inline would have to abandon a half-written section 11.
type resolvedData struct {
	passive bool
	mem     uint32
	// offset is the const expression's bytes **including** its `0x0b` terminator — `const c` is
	// `list instr c.it; end_ ()` (encode.ml:912-913), the same explicit terminator a function body
	// gets and which the text does not spell.
	offset []byte
	bytes  []byte
}

// textElem is one element segment as the parse read it: the mode, an unresolved table index, the
// retained offset, and the element list in whichever of the two forms the text used.
//
// **`table` is unresolved for `textData.mem`'s reason**, and the argument transfers without change:
// `encode.ml`'s `elem` splits its active arms on the *resolved* index — `Active ({it = 0l; _}, c)`
// takes the `0x00`/`0x04` form and `Active (x, c)` the `0x02`/`0x06` form (:1068-1082) — so the
// question is "is this index zero", never "did the text write a `(table …)`". A symbolic
// `(table $t)` naming table 0 is the case that makes it more than pedantic.
//
// **One element list, not two, and the wire form is *derived* rather than remembered — which is the
// opposite of what this comment said when it was written.** The claim was that the reference's
// discriminator cannot be reconstructed here, so the two spellings had to be kept apart as a
// retained flag. That is false, and reading `encode.ml`'s executable rather than reasoning from the
// grammar is what said so:
//
//	if is_elem_kind rt && List.for_all is_elem_index cs then (* the index forms, flags 0/1/2/3 *)
//	else (* the expression forms, flags 4/5/6/7 *)                             (encode.ml:1064-1084)
//
// Neither half of that predicate asks which grammar arm ran. `is_elem_kind` is `(NoNull, FuncHT)`
// (:1044-1046) — a question about the reftype's *nullability*, and `valType.null` retains exactly
// that, because a text valType is a parsed type and not the wire byte. (At the time this was
// written, `binary.ValType` was the byte that lost it; the confusion was between the two
// representations, one package apart. 0018 later widened `binary.ValType` to carry nullability
// too, which does not change this package's own reasoning — `valType.null` is still the field
// this predicate reads, and it was never `binary.ValType` in the first place.)
// `is_elem_index` is `[{it = RefFunc _}]` — a question about each element *expression's shape*, and
// `elemidx_list` builds precisely those: `List.map (fun x -> [ref_func x])` (parser.mly:1147-1150).
//
// So the grammar's two spellings are not the format's two forms, and keeping a flag would encode the
// *spelling*. Measured over the corpus — 1385 `(elem` forms across the 265 `.wast` files under
// `third_party/spec/test`, which is the population every count in this paragraph is against — that
// mis-encodes both directions:
//
//   - `(elem funcref (ref.func …))` — 450 forms, the commonest spelling — reads as
//     `reftype elemexpr_list`, and `funcref` is `(Null, FuncHT)`, so `is_elem_kind` is **false** and
//     the reference writes the expression family. A spelling-keyed flag writes the index family.
//   - `(elem (ref func) (ref.func 0))` — 4 forms, `elem.wast:539` among them — reads the same arm,
//     and `(ref func)` is `(NoNull, FuncHT)`, so the predicate is **true** and the reference writes
//     the *index* family. A spelling-keyed flag writes the expression family.
//
// **The two counts were 683 and 8 until they were re-run, and the correction is the point of naming
// the population.** Neither figure was reproducible: the census that produced them globbed a
// different file set and binned `(elem $f)` under a `table` parent as an anonymous segment. Nothing
// downstream moved — the argument is that the predicate asks about content, and it needs the
// *existence* of both directions rather than their sizes — which is exactly the condition under which
// a number goes unchecked. Stated with its population so the next reader can re-run it.
//
// Both spellings would decode clean, denoting the same module with different bytes. The same defect
// `textData.mem` documents at length, one section over: the discriminator is a question about
// *content*, never about what the text spelled. See `encodeElems` for why the "and would diverge from
// wabt" clause this paragraph used to carry was measuring a feature gate rather than a producer.
//
// The 0016 sentence that stands is the general one, and it is what forces this shape: **retention is
// forced by consumers, but shaped by the grammar.** The grammar's element list is
// `elemexpr_list`, a list of constant expressions — `elemidx_list` is *sugar that expands into one*
// — so one list of expressions is the wire form, and the encoder derives the flag from it.
type textElem struct {
	mode elemMode
	// table is the table index, meaningless unless the mode is active. The zero value is the sugar
	// arm's default, the reference's `Active (0l @@ $sloc, …)`.
	table idxRef
	// offset is the active arm's offset expression, retained at the cursor by elemOffset.
	offset instrSink
	// elemType is the segment's element type. The default is `funcref` — `(Null, FuncHT)`, spelled
	// by the abbreviation — because that is what the two `elemkind`/`elemidx_list` arms mean
	// (parser.mly:1136, :1175), and *not* a zero value standing in for "unwritten": the whole point
	// of the derivation above is that `funcref` and `(ref func)` are different types choosing
	// different flags, so a default that could not tell them apart would be the bug.
	elemType valType
	// elems is the element list, one retained constant expression per element, in source order.
	//
	// The index spelling `(elem func $a $b)` lands here too, as the synthesized `ref.func` sink
	// `elemidx_list` expands it into (`elemIdxSink`). That is the reference's own expansion rather
	// than a normalization invented here, which is what makes `is_elem_index` answerable on the
	// other side.
	elems []instrSink
}

// elemKindFuncref is `elemkind`'s only value: `(NoNull, FuncHT)`, the reference's `FUNC { (NoNull,
// FuncHT) }` (parser.mly:1136) and the `let rt = (NoNull, FuncHT) in` the bare-index sugar states
// outright (:1177).
//
// **A named constant rather than a literal at the three sites that mean it**, because it is exactly
// `is_elem_kind`'s true case (encode.ml:1044-1046) and one letter of it decides the wire form: written
// `null: true` it is `funcref`, which `is_elem_kind` rejects, and every `(elem func $f)` in the corpus
// would take flag 4 instead of flag 0 — 423 occurrences of the `elemkind` spelling plus 118 of the
// bare-index sugar. There is no spelling of that mistake the suite can see: both images decode, and
// both denote the same module.
//
// Note it is *not* the type `funcref` spells, and the two are one field apart — see
// `abbreviatedReftypes`, where `kwFuncref` expands to `(Null, FuncHT)`. A text `(elem funcref
// (ref.func 0))` and a text `(elem func 0)` denote the same segment and encode differently, which is
// the whole content of `textElem`'s derivation argument.
var elemKindFuncref = valType{heap: heapRef{abs: kwFunc}}

// elemMode is an element segment's mode: the three arms of the reference's `segmentmode`.
//
// Three where `textData` has two, for the reason binary.ElemMode records: `Declarative` is an
// *error* for a data segment and a real mode for an element one, with its own spelling in the text
// grammar (`(elem declare …)`, parser.mly:1167).
type elemMode byte

const (
	// elemActive is the zero value deliberately, matching wire flags 0 — the mode the smallest
	// encoding means, so a segment whose mode nothing wrote reads correctly.
	elemActive elemMode = iota
	elemPassive
	elemDeclarative
)

// resolvedElem is an element segment after stage 2: the table index resolved, the offset and every
// element expression encoded, so the writer cannot fail.
//
// Distinct from `textElem` for `resolvedData`'s reason — an offset or an element expression can hold
// a `global.get $g` naming a global defined later, so its bytes are not knowable at the cursor, and
// a writer that resolved inline would have to abandon a half-written section 9.
// **The element list is kept in *both* renderings, and that is the encode-side face of the form
// derivation `textElem` documents.** A segment taking one of the index forms writes
// `vec elem_index cs`, a vector of bare function indices with no `ref.func` and no terminator; one
// taking an expression form writes `vec const cs`, a vector of encoded expressions. The flag decides
// which, and the flag is not knowable until every element is resolved — so both renderings are
// produced in stage 2 and the writer picks, on the discipline every other section here follows: once
// bytes start being written, nothing can fail.
type resolvedElem struct {
	mode     elemMode
	table    uint32
	offset   []byte
	elemType resolvedVal
	// exprs is one encoded const expression per element, each **including** its `0x0b` terminator —
	// `vec const cs` (encode.ml:1078), where `const` is `list instr c.it; end_ ()`.
	exprs [][]byte
	// funcs is the same list rendered as `elem_index` would render it: for each element, the encoded
	// function index if that element is exactly `ref.func x`, and `isIdx` false otherwise.
	//
	// This is `is_elem_index`/`elem_index` (encode.ml:1052-1060) answered where the expression's
	// instructions are still in hand, rather than re-derived at the writer from the encoded bytes — a
	// writer that pattern-matched `d2 00 0b` back apart to recover `0` would be a second decoder
	// living inside the encoder.
	funcs []elemIdx
}

// elemIdx is one element's `is_elem_index`/`elem_index` answer: whether the element is exactly
// `ref.func x`, and the encoded `x` when it is.
//
// **The index is kept as *bytes*, and that is what makes this a projection rather than a decode.**
// `elem_index` is `idx x` (encode.ml:1059) and `idx` is `u32 x.it` (:102) — the identical encoding
// `ref.func`'s own immediate gets, since that arm is `op 0xd2; idx x` (:415). So the bytes the index
// form writes are byte-for-byte the immediate the expression form already holds, and the two
// renderings differ only in what surrounds them: the index form drops the opcode and the terminator.
// Keeping the LEB means nothing here parses one, which is the cheaper *and* the safer of the two —
// the numeric value is never needed, so a reader that could disagree with the writer is never
// written.
//
// A struct rather than a `[][]byte` plus a flag, because the predicate is **per element** and the
// reference folds it with `List.for_all` — a segment mixing `(ref.func 0)` with `(ref.null func)` has
// a true answer for one element and a false one for the other, and `bulk.wast:12` is exactly that
// module. The fold is the writer's; keeping the per-element answer is what lets it run.
type elemIdx struct {
	// imm is the encoded function index — the same LEB bytes as the `ref.func` immediate, nil unless
	// isIdx.
	imm   []byte
	isIdx bool
}

// resolvedVal is a valType with its heap reference resolved to an index — comparable with ==,
// which is the whole point of having a second representation.
type resolvedVal struct {
	num   string
	null  bool
	abs   keywordKind
	idx   uint32
	isIdx bool
}

// isElemKind is `is_elem_kind` (encode.ml:1044-1046): whether this segment's element type is exactly
// `(NoNull, FuncHT)`, the type `(ref func)` spells and `funcref` does not.
//
// **A method on `resolvedElem` rather than on `resolvedVal`, because it is a question about a segment's
// eligibility for the index forms and not a general fact about a type.** The distinction is not
// pedantry: `isFuncref` below is the *other* nullability, wanted by flag 0x04's guard, and two
// same-shaped predicates on the same struct differing only in a bool are exactly the pair a reader
// merges by accident. Keeping them apart at the type they are asked *about* makes the merge visible.
func (e resolvedElem) isElemKind() bool {
	return !e.elemType.null && e.elemType.abs == kwFunc && !e.elemType.isIdx && e.elemType.num == ""
}

// allElemIndex is `List.for_all is_elem_index cs` (encode.ml:1052-1055, folded at :1064): whether every
// element expression is exactly `ref.func x`.
//
// **The empty list is true, and that is the reference's `for_all` rather than a convenience.** A
// segment with no elements takes the *index* family — `(elem func)` writes flag 1 with an empty
// vector — because `for_all` over `[]` is vacuously true in OCaml as in the definition of the
// quantifier. So the 29 corpus rows that reach an empty element list, and `(elem func)` beside them,
// all encode in four bytes rather than five. Stated because a hand-written loop returning false on
// empty is the easy inversion, and no vector distinguishes the two: both decode to a segment with no
// elements, one of them from bytes the reference never writes.
func (e resolvedElem) allElemIndex() bool {
	for _, f := range e.funcs {
		if !f.isIdx {
			return false
		}
	}
	return true
}

// isFuncref is `rt = (Null, FuncHT)` — flag 0x04's guard (encode.ml:1079), and the complement of
// `isElemKind` on the one field that matters.
//
// The two are not each other's negation over all types, which is why both exist: `externref` is
// neither, and a segment holding one at table 0 takes 0x06 rather than either short form.
func (v resolvedVal) isFuncref() bool {
	return v.null && v.abs == kwFunc && !v.isIdx && v.num == ""
}

// resolvedTable is a table definition whose element type has been resolved — what the encoder writes.
//
// Distinct from `tabType` for exactly the reason `resolvedComp` is distinct from `compType`: one is
// what the parse read, the other is what the deferred phase produced, and collapsing them would put
// a field in the struct that is meaningful only after a phase the type does not mention.
type resolvedTable struct {
	addr64 bool
	lim    limits
	elem   resolvedVal
}

// idxRef is a `typeuse`'s operand (parser.mly:470-471, `idx` at :487-489), kept unresolved.
//
// **The two arms fail differently and the reference's messages say so.** `idx`'s NAT arm is
// `nat32 $1` with *no lookup* — a number is never `unknown type $name`; it becomes `unknown type
// <n>` only later, from `func_type`'s out-of-range access. The VAR arm is `lookup c (var …)`,
// which raises `unknown type $name` with the name printed. So a numeric index that is out of
// range and a symbolic one that is unbound produce different text, and both are the reference's.
type idxRef struct {
	tok   Token
	isVar bool
	name  string // the decoded identifier, when isVar
	idx   uint32 // the parsed index, when !isVar
}

// String renders a resolved value type in the *text* format's spelling.
//
// For diagnostics only, and specifically for the encoder's frontier messages (#8), which name a
// type the emitter has no byte for. It renders wat rather than a Go struct dump because the reader
// is holding wat — and it renders it in the reference's own spelling, `(ref null $t)` and the
// abbreviations, so a message quoting a type quotes something the user could have typed.
//
// **Not the reference's `string_of_val_type`, and not claiming to be.** That renders a *resolved*
// index as `(ref null 3)` after the parse has forgotten which `$name` produced it, which is exactly
// what this does — the identifier is gone by this point, by design (resolveTypeIdx keeps the index).
// So a name the user wrote comes back as a number here. That is honest for a frontier message and
// would be wrong for an error the suite matches; nothing here is matched by the suite.
func (v resolvedVal) String() string {
	if v.num != "" {
		return v.num
	}
	if v.isIdx {
		if v.null {
			return fmt.Sprintf("(ref null %d)", v.idx)
		}
		return fmt.Sprintf("(ref %d)", v.idx)
	}
	// The absolute heap types, spelled through heapWat — which is scoped to exactly these twelve
	// and whose comment has the measurement showing why the general version of that derivation is
	// false. This is the one diagnostic in the package with no token to quote: resolveVal keeps
	// `abs` and drops the keyword.
	name := heapWat(v.abs)
	if v.null {
		return "(ref null " + name + ")"
	}
	return "(ref " + name + ")"
}

// heapString renders a resolved value type as a *bare heap type* — `func`, not `(ref func)`.
//
// **A separate renderer because `String` above is wrong for this position, not merely verbose.**
// `ref.null`'s immediate is a bare `heaptype` (encode.ml:414), which has no nullability field, so
// `String`'s spelling names a *reftype* — and for a heap type read out of `heaptype`, `null` holds
// whatever that production's caller happened to leave there. A message reading "the heap type
// `(ref func)`" quotes a construct at the wrong level and asserts a nullability the grammar never
// supplied: the invented-evidence shape (grave #36), in the half of an error no expected string
// covers.
//
// The two are deliberately kept as separate methods on the same struct rather than one with a flag,
// for the reason `isElemKind` and `isFuncref` are: two renderings differing only in whether one
// field is read are exactly the pair a later reader merges by accident, and keeping them apart at
// the *question* makes the merge visible.
//
// The `num` arm cannot arise — `resolveVal` only produces `num` from `valType.num`, and a heaptype
// carries none — so it is folded into the index arm rather than given a branch that no input
// reaches. The index arm prints the number for the reason `String`'s comment gives: the identifier
// is gone by resolution, by design.
func (v resolvedVal) heapString() string {
	if v.abs == "" {
		return strconv.FormatUint(uint64(v.idx), 10)
	}
	return heapWat(v.abs)
}

// resolvedFunc is a functype whose value types are all resolved.
type resolvedFunc struct {
	params  []resolvedVal
	results []resolvedVal
}

func (a resolvedFunc) equal(b resolvedFunc) bool {
	return slices.Equal(a.params, b.params) && slices.Equal(a.results, b.results)
}

// resolvedStorage is a storageType with its value type resolved (decision 0021).
//
// Not `binary.StorageType` reused directly — `resolvedVal` is this package's own reason for
// existing rather than reusing `binary.ValType` (its doc comment states it, and 0021's task notes
// the same argument transfers to a field's storage): `val` is meaningful only `!packed`, and
// `valTypeBytes` is what turns it into wire bytes, exactly as a functype param's `resolvedVal`
// does. `packed`/`width` need no such translation — a packed storage's two wire bytes are fixed
// (`-0x08`/`-0x09`), so they are carried as-is rather than through an intermediate resolvedVal
// shape that has no case for them.
type resolvedStorage struct {
	val    resolvedVal // meaningful when !packed
	packed bool
	width  byte // 8 or 16, meaningful only when packed
}

// resolvedField is a fieldType with its storage resolved: one struct or array field, ready to
// encode (decision 0021).
type resolvedField struct {
	storage resolvedStorage
	mut     bool
}

// resolvedComp is a compType with its content resolved: the functype for `compFunc`, the field
// list for `compStruct`/`compArray` (decision 0021).
//
// **No `fieldNames` field, and that is worth stating because `compType` has one (#188).** A field
// *name* binds to a position and never to a type index, so there is nothing about it that a
// forward reference could leave unresolved — unlike `fields`/`ft`, whose value types may name a
// type defined later, `structtype`'s name-to-index binding is complete the moment its `(type …)`
// field finishes parsing. `resolveFieldIdx`'s one caller (`structFieldPairRetained`) reads it
// straight off `p.ctx.typeDefs`, before `runDeferred` — and therefore before this type's own
// values exist — because a symbolic field name appears inside a function body, which this
// stratum parses in the same pass that appends to `typeDefs`, not in the deferred phase that
// builds `typeCtx`. Carrying the map here too would be a second, always-stale-until-copied
// place holding a fact `typeDefs` already answers.
type resolvedComp struct {
	kind   compKind
	ft     resolvedFunc
	fields []resolvedField

	// final and supertypes are compType's own two facts, resolved: supertypes as plain type
	// indices (resolveTypeIdx, the same lookup a numeric or symbolic typeuse always goes
	// through), final unchanged — there is nothing to resolve in a bool.
	final      bool
	supertypes []uint32
}

// defineType records an explicit type definition's content at its index.
//
// Called from `typeDef` as the definition is read, which is the reference's stage 1 — and stage 1
// walks the field list in source order doing nothing else, so recording during the parse is the
// same table. The name was already bound by bindidxOpt, at stage 0.
func (c *context) defineType(ct compType) { c.typeDefs = append(c.typeDefs, ct) }

// defineMemory records a defined memory's type at its index (#8).
//
// Not deferred, and that is a grammar fact rather than a shortcut: a `memorytype` is `addrtype
// limits` and neither half can name a type, so there is nothing in it to resolve. The reference's
// `memory_fields` arm still runs in stage 2, but stage is only observable where a *lookup* happens.
//
// **Defined memories only.** An imported memory occupies the memory index space and belongs to the
// import section, not the memory section — the same population split `decodeTableForm`'s comment
// names on the other side, where merging them "would make an import look like a definition".
func (c *context) defineMemory(mt memType) { c.memDefs = append(c.memDefs, mt) }

// defineTable records a defined table, deferring its element type's resolution.
//
// Deferred because `table_fields` runs inside `module_fields1`'s *second* `fun () ->`
// (parser.mly:1341-1347) and its `tabletype` is `$1 c` — a context lookup — so
// `(table 1 (ref null $t)) (type $t (func))` is a valid module whose reftype resolves against a
// type defined later. Resolving at the cursor would reject it: the accept-direction failure §9 G-3
// names, and the same one typetable.go's header has `imports.wast:62` for.
//
// **The slot is appended now and filled later**, rather than appending inside the thunk, because
// stage-2 order is *field* order and a table's own thunk runs at its own position — so the two
// orders agree — but a reader should not have to prove that to know the table's index. The index is
// the append's position, established during the parse where it is obvious.
//
// This also closes a missing reject that the retention forces rather than chooses:
// `(table 1 (ref null $undefined))` was accepted before this, because the reftype's value was
// discarded and with it the lookup. Measured, and the suite has no vector for it — `ref.wast:42`
// spells the numeric form as `assert_invalid`, the validator's, so nothing on the board could see
// the symbolic one.
func (c *context) defineTable(tt tabType) {
	i := len(c.tabDefs)
	c.tabDefs = append(c.tabDefs, resolvedTable{addr64: tt.addr64, lim: tt.lim})
	c.deferOp(func() error {
		rv, err := c.resolveVal(tt.elem)
		if err != nil {
			return err
		}
		c.tabDefs[i].elem = rv
		return nil
	})
}

// defineGlobal records a defined global, deferring its type and its initializer to stage 2 (#8).
//
// **`defineData`'s shape, copied rather than re-derived** — *lessons are indexed by shape* (#105) — and
// both halves of the deferral are load-bearing for reasons the siblings already argue:
//
//   - the **type** can name a type index that forward-references, which is `defineTable`'s argument
//     verbatim (`(global (ref null $t) …) (type $t (func))`), so resolving at the cursor would reject a
//     valid module in the direction no vector speaks to (§9 G-3);
//   - the **initializer** can be `global.get $g` naming an *imported* global bound later in the field
//     list, and `global.wast:9`'s `(global $x (mut i32) (global.get $y))` shape is the reference's own
//     — `module_fields1` evaluates every global field inside the second `fun () ->`, so `$y` need not be
//     bound when the initializer is read.
//
// The initializer's instructions were retained at the *cursor*, by `globalField` calling `intoSink`,
// and this thunk only encodes them: an instruction's position in the expression cannot go in a thunk
// (`instr`'s comment), and each instruction's own symbolic index defers individually through
// `instr.patch`. Same division of labour as `defineData`'s offset.
//
// **Defined globals only.** An imported global occupies the global index space and belongs to section
// 2 — the population split `defineMemory`'s comment names, and the reason the import path builds a
// `resolvedGlobal` through `importedGlobal` instead of arriving here.
func (c *context) defineGlobal(g textGlobal) {
	i := len(c.globalDefs)
	c.globalDefs = append(c.globalDefs, resolvedGlobalDef{})
	c.deferOp(func() error {
		rv, err := c.resolveVal(g.typ.val)
		if err != nil {
			return err
		}
		c.globalDefs[i].typ = resolvedGlobal{val: rv, mut: g.typ.mut}
		init, err := c.constExprBytes(g.init)
		if err != nil {
			return err
		}
		c.globalDefs[i].init = init
		return nil
	})
}

// defineImport records one import, deferring whatever inside it needs the complete type table (#8).
//
// **The slot is appended at the parse position and filled by a thunk**, which is `defineTable`'s
// shape and is here for a stronger reason than there. An import's descriptor can contain a *type
// index* — `(import "a" "b" (func (type $t)))` — and a type index is the one thing in a module that
// forward-references by design (`imports.wast:62`, the vector typetable.go's header is built on). So
// resolving at the cursor would reject valid modules, and appending inside the thunk would make the
// import's position in the section depend on stage-2 order rather than on source order. Neither is
// acceptable; splitting them gets both.
//
// The caller passes a `fill` that runs in stage 2 and returns the descriptor. It receives no index:
// an import's descriptor never depends on which import it is, and passing the index would invite a
// thunk that reads a *later* import's slot — the ordering trap this split exists to close.
func (c *context) defineImport(module, name string, fill func() (importDesc, error)) {
	i := len(c.imports)
	c.imports = append(c.imports, textImport{module: module, name: name})
	c.deferOp(func() error {
		d, err := fill()
		if err != nil {
			return err
		}
		c.imports[i].desc = d
		return nil
	})
}

// defineData records one data segment, deferring its memory index to stage 2 (#8).
//
// **Slot appended at the parse position, filled by a thunk** — `defineImport`'s shape, copied rather
// than re-derived (*lessons are indexed by shape*), and load-bearing here for the export arm's reason
// rather than the import arm's: a data segment's memory index forward-references, because
// `module_fields1` evaluates every data field inside the second `fun () ->`. Measured on the
// reference's grammar, not assumed — `(module (data (memory $m) (offset (i32.const 0)) "") (memory $m 1))`
// binds `$m` after the segment reads it, and resolving at the cursor would reject it. Accept
// direction, and the suite has nothing to say: `data.wast`'s `unknown memory` vectors are all
// numeric, hence `assert_invalid` and the validator's (§9 G-3).
//
// The **offset's own instructions** are not deferred here, because they were already retained at the
// cursor by `dataOffset` — an instruction's *position* in the expression is the one thing that cannot
// go in a thunk (`instr`'s comment), and each instruction's symbolic index defers individually
// through `instr.patch`. So this thunk resolves the segment's memory index and encodes the retained
// list; nothing about the list's shape is decided in stage 2.
func (c *context) defineData(d textData) {
	i := len(c.dataDefs)
	c.dataDefs = append(c.dataDefs, resolvedData{passive: d.passive, bytes: d.bytes})
	c.deferOp(func() error {
		if !d.passive {
			idx, err := c.memories.resolveSpaceIdx(d.mem)
			if err != nil {
				return err
			}
			c.dataDefs[i].mem = idx
		}
		off, err := c.constExprBytes(d.offset)
		if err != nil {
			return err
		}
		c.dataDefs[i].offset = off
		return nil
	})
}

// defineElem records one element segment, deferring its table index and its element indices to
// stage 2 (#8, 0016).
//
// **`defineData`'s shape, copied rather than re-derived** — *lessons are indexed by shape* — and the
// forward-reference argument is the same one, with a second instance of it: not only can the table
// index name a table defined later, but so can every *element* index name a function defined later,
// which `elem.wast:12`'s `(elem (i32.const 0) $f)` before `(func $f)` does. Resolving at the cursor
// would reject those in the accept direction, where the suite has nothing to say.
//
// The offset's and each element expression's instructions are **not** deferred here — they were
// retained at the cursor by elemOffset and elemexpr, for `defineData`'s reason: an instruction's
// position in an expression is the one thing that cannot go in a thunk. This thunk resolves indices
// and encodes the retained lists; nothing about their shape is decided in stage 2.
func (c *context) defineElem(e textElem) {
	i := len(c.elemDefs)
	c.elemDefs = append(c.elemDefs, resolvedElem{mode: e.mode})
	c.deferOp(func() error {
		if e.mode == elemActive {
			idx, err := c.tables.resolveSpaceIdx(e.table)
			if err != nil {
				return err
			}
			c.elemDefs[i].table = idx
			off, err := c.constExprBytes(e.offset)
			if err != nil {
				return err
			}
			c.elemDefs[i].offset = off
		}
		et, err := c.resolveVal(e.elemType)
		if err != nil {
			return err
		}
		c.elemDefs[i].elemType = et
		// Each element is rendered **both** ways, because which rendering the wire takes is a
		// question about the whole list and cannot be answered per element — see `resolvedElem`.
		for _, sink := range e.elems {
			b, err := c.constExprBytes(sink)
			if err != nil {
				return err
			}
			c.elemDefs[i].exprs = append(c.elemDefs[i].exprs, b)
			c.elemDefs[i].funcs = append(c.elemDefs[i].funcs, elemIdxOf(sink))
		}
		return nil
	})
}

// elemIdxOf is `is_elem_index` and `elem_index` in one answer (encode.ml:1052-1060): whether this
// element is *exactly* `ref.func x`, and the `x` when it is.
//
// **`len == 1` is the reference's own test and it is a test on the instruction list, not on the
// bytes.** `[{it = RefFunc x; _}]` matches a one-instruction expression and nothing else, so
// `(item (ref.func 0) (ref.func 0))` — a two-instruction constant expression, ill-typed but
// well-formed to the grammar — takes the expression form, and `(item)` with no instructions does too.
// The terminator is not in the list (`constExprBytes` appends it), which is why this counts 1 where
// the interpreter's decoded side counts 2: two representations of the same expression, one before the
// terminator is written and one after it is read. (The interpreter's reader used to be
// `constExprRef`, whose `len(expr) == 2` was the other end of this fact; #241 replaced it with a
// general evaluator, so the 2 is now `opEnd` arriving at the interpreter's loop rather than a length
// a matcher tests. The off-by-one between the two packages is unchanged.)
//
// A symbolic `$f` is an unresolved index at the cursor and a resolved one here, which is the whole
// reason this runs inside the thunk: `retainIdx` deferred it through `patch`, so the bytes exist only
// after the patch runs. Rather than decode the patched immediate back, this re-runs the same
// resolution the patch does — which is not a second reading of the protocol, because both go through
// the same `instr.patch` closure. The alternative, pattern-matching `d2 <leb>` out of the encoded
// bytes, would put a decoder in the encoder.
func elemIdxOf(s instrSink) elemIdx {
	if len(s.instrs) != 1 {
		return elemIdx{}
	}
	in := s.instrs[0]
	op, ok := opBytes("ref.func")
	if !ok {
		// Unreachable: `ref.func` is in the generated table (opcodes.go, 0xd2), which
		// TestElemIndexFormNeedsExactlyRefFunc pins by round-tripping a segment that takes the
		// index form. A false answer here would silently move every index-form segment to the
		// expression form — a legal image, different bytes, and no vector to say so.
		panic("text: ref.func has no opcode, so an element segment's wire form cannot be derived")
	}
	if !bytes.Equal(in.op, op) {
		return elemIdx{}
	}
	imm := in.imm
	if in.patch != nil {
		b, err := in.patch()
		if err != nil {
			// The same resolution `defineElem`'s own `constExprBytes` call is about to run and
			// report, so this arm cannot be the first to see the error: an unbound `$f` fails
			// there with the token quoted. Answering "not an index" here leaves the expression
			// form selected for an element that is about to be rejected anyway.
			return elemIdx{}
		}
		imm = b
	}
	return elemIdx{imm: imm, isIdx: true}
}

// constExprBytes encodes a retained const expression, terminator included.
//
// `const c` is `list instr c.it; end_ ()` (encode.ml:912-913), so the `0x0b` is the encoder's and not
// the text's — the same explicit terminator `writeCodeSection` appends to every body, and for the
// same reason: `constexpr` has no `end` token in the grammar.
//
// **The same patch protocol a function body uses**, read off the one loop in `resolveFuncs` rather
// than reinvented: a patch *replaces* the immediates rather than appending to them, which is the
// property `retainIdx` refuses a second deferred index to preserve. Two loops now know that
// protocol, which is a drift risk with a name — `TestConstExprBytesMatchesABodysEncoding` holds them
// to the same output for the same instruction list, so the second reader cannot quietly grow a third
// interpretation of `patch`.
//
// A passive segment has no offset and calls this with an empty sink, which correctly yields a bare
// `0x0b`; the caller does not write it, so the byte is never emitted. Stated because "encodes to one
// byte rather than zero" reads like a defect until you know nobody consumes it.
func (c *context) constExprBytes(s instrSink) ([]byte, error) {
	var w writer
	for _, in := range s.instrs {
		w.bytes(in.op)
		imm := in.imm
		if in.patch != nil {
			b, err := in.patch()
			if err != nil {
				return nil, err
			}
			imm = b
		}
		w.bytes(imm)
	}
	w.byte1(opEnd)
	return w.b, nil
}

// defineExport records one export, deferring the index resolution to stage 2.
//
// **Read defineImport above before this**, which is the sibling shape: slot appended at the parse
// position, filled by a thunk. Copied rather than re-derived, per the lessons-are-indexed-by-shape
// rule — and the deferral is load-bearing for a *different* reason here. An import's forward
// reference is to a type; an export's is to the thing it exports, and the suite proves it:
//
//	exports.wast:14  (module (export "a" (func $a)) (func $a))
//
// `$a` is not bound when the export is read. The reference gets this from `module_fields1`'s export
// arm evaluating `$1 c` inside the innermost `fun () ->` (parser.mly:1382-1384), by which time
// every field has bound its names. Resolving at the cursor would reject that module — the accept
// direction, where no vector will tell us (§9 G-3).
//
// The thunk returns the resolved index rather than taking one, matching defineImport's argument
// about not letting a thunk read a neighbouring slot.
func (c *context) defineExport(name string, kind importKind, resolve func() (uint32, error)) {
	i := len(c.exports)
	c.exports = append(c.exports, textExport{name: name, kind: kind})
	c.deferOp(func() error {
		idx, err := resolve()
		if err != nil {
			return err
		}
		c.exports[i].idx = idx
		return nil
	})
}

// deferOp records one stage-2 operation. See the file header for why parse order is stage-2 order.
func (c *context) deferOp(f func() error) { c.deferred = append(c.deferred, f) }

// runDeferred resolves the explicit type table and then runs the recorded operations in order.
//
// The two steps are separate because every operation may read any entry of the table, including
// entries defined after the field that uses them — which is the forward reference the whole phase
// exists for. Nothing is resolved until every name is bound.
func (c *context) runDeferred() error {
	c.typeCtx = make([]resolvedComp, 0, len(c.typeDefs))
	for _, ct := range c.typeDefs {
		supertypes, err := c.resolveSupertypes(ct.supertypes)
		if err != nil {
			return err
		}
		switch ct.kind {
		case compFunc:
			ft, err := c.resolveFunc(ct.ft)
			if err != nil {
				return err
			}
			c.typeCtx = append(c.typeCtx, resolvedComp{
				kind: compFunc, ft: ft, final: ct.final, supertypes: supertypes,
			})
		case compStruct, compArray:
			fields, err := c.resolveFields(ct.fields)
			if err != nil {
				return err
			}
			c.typeCtx = append(c.typeCtx, resolvedComp{
				kind: ct.kind, fields: fields, final: ct.final, supertypes: supertypes,
			})
		}
	}
	// Indexed rather than ranged: an operation may append an implicit type, and while nothing
	// appends an *operation*, a range over a slice that grows is a trap worth not planting. A
	// `range` over an integer would re-read `len` never, and over the slice would snapshot it —
	// this re-reads it each step, which is the whole reason for the shape.
	//nolint:intrange // the bound is re-evaluated deliberately; see above
	for i := 0; i < len(c.deferred); i++ {
		if err := c.deferred[i](); err != nil {
			return err
		}
	}
	return nil
}

// resolveVal resolves one value type's heap reference.
func (c *context) resolveVal(v valType) (resolvedVal, error) {
	if v.num != "" {
		return resolvedVal{num: v.num}, nil
	}
	if v.heap.abs != "" {
		return resolvedVal{null: v.null, abs: v.heap.abs}, nil
	}
	idx, err := c.resolveTypeIdx(idxRefFromToken(v.heap.tok))
	if err != nil {
		return resolvedVal{}, err
	}
	return resolvedVal{null: v.null, idx: idx, isIdx: true}, nil
}

func (c *context) resolveFunc(ft funcType) (resolvedFunc, error) {
	out := resolvedFunc{}
	for _, group := range []struct {
		src []valType
		dst *[]resolvedVal
	}{{ft.params, &out.params}, {ft.results, &out.results}} {
		for _, v := range group.src {
			rv, err := c.resolveVal(v)
			if err != nil {
				return resolvedFunc{}, err
			}
			*group.dst = append(*group.dst, rv)
		}
	}
	return out, nil
}

// resolveStorage resolves one field's storage type (decision 0021): a value type through the
// same `resolveVal` a functype's param already uses — so `(field (ref $t))` may forward-reference
// a type defined later in the field list, exactly as `(param (ref $t))` does — or a packed width
// carried through unchanged, having no index of its own to resolve.
func (c *context) resolveStorage(st storageType) (resolvedStorage, error) {
	if st.packed {
		return resolvedStorage{packed: true, width: st.width}, nil
	}
	v, err := c.resolveVal(st.val)
	if err != nil {
		return resolvedStorage{}, err
	}
	return resolvedStorage{val: v}, nil
}

// resolveFields resolves a struct's or array's field list (decision 0021), in declaration order —
// `resolveFunc`'s shape, copied rather than re-derived, for a field list rather than a param list.
func (c *context) resolveFields(fields []fieldType) ([]resolvedField, error) {
	out := make([]resolvedField, 0, len(fields))
	for _, f := range fields {
		st, err := c.resolveStorage(f.storage)
		if err != nil {
			return nil, err
		}
		out = append(out, resolvedField{storage: st, mut: f.mut})
	}
	return out, nil
}

// resolveSupertypes resolves a subtype's declared supertype list (0019's own named gap), one
// `resolveTypeIdx` lookup per entry — the same lookup a numeric or symbolic typeuse always goes
// through, so a supertype named by `$name` and one named by a bare index resolve identically.
//
// A nil input reports a nil output rather than an empty non-nil slice, matching `binary.CompType`'s
// own convention (`Supertypes` is empty for a bare comptype with no `sub` wrapper) and letting the
// interpreter's declared-supertype walk treat "no wrapper" and "wrapper, no supertypes" the same
// way it already must — both are zero-length.
func (c *context) resolveSupertypes(refs []idxRef) ([]uint32, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	out := make([]uint32, 0, len(refs))
	for _, r := range refs {
		idx, err := c.resolveTypeIdx(r)
		if err != nil {
			return nil, err
		}
		out = append(out, idx)
	}
	return out, nil
}

// resolveTypeIdx is the reference's `type_ c x` = `lookup "type" c.types.space x`
// (parser.mly:152/:164).
//
// Only the VAR arm can fail here, and the message carries the identifier. It is printed from the
// token's own text rather than reconstructed from the decoded name: the reference's `print` (:145)
// re-quotes a name whose decoded form differs from its spelling, and reproducing that rendering
// from a decoded value is how grave #36 came to quote a byte the input never held.
func (c *context) resolveTypeIdx(r idxRef) (uint32, error) {
	if !r.isVar {
		return r.idx, nil
	}
	i, ok := c.types.names[r.name]
	if !ok {
		return 0, errf(r.tok, "unknown type %s", r.tok.Text)
	}
	return i, nil
}

// funcTypeAt is the reference's `func_type c x` (parser.mly:164-168), taking an index its caller
// has already resolved.
//
// Three outcomes and all three are the reference's, in its order: the index resolves and names a
// functype; it resolves and names a struct or array (`non-function type <n>`); it does not resolve
// (`unknown type <n>`, from `Lib.List32.nth` raising `Failure`). The number in both messages is
// `Int32.to_string x.it` — the *index*, after any name lookup, not the identifier — which is why
// this prints idx rather than the token.
//
// **It takes the index rather than the idxRef because the two lookups are separately timed.** The
// name lookup is `typeuse`'s (`$3 c type_`, :471) and happens whenever a typeuse is written; the
// *range* check is this function's and is skipped in the deferred case. Threading a idxRef in here
// and resolving it locally would fuse them, which is the defect inlineFuncTypeExplicit's header
// describes.
func (c *context) funcTypeAt(tok Token, idx uint32) (resolvedFunc, error) {
	if idx >= uint32(len(c.typeCtx)) {
		return resolvedFunc{}, errf(tok, "unknown type %d", idx)
	}
	e := c.typeCtx[idx]
	if e.kind != compFunc {
		return resolvedFunc{}, errf(tok, "non-function type %d", idx)
	}
	return e.ft, nil
}

// inlineFuncType is the reference's `inline_functype` (parser.mly:222-235): the index of an
// implicit type with this signature, reusing a structurally equal existing one or appending.
//
// **The return value used to be discarded by every caller, and the comment here argued for keeping
// it anyway**: "nothing in a reject-only parser reads a type index… It is *not* dropped from the
// signature, because the number is the whole observable effect: appending shifts `len(typeCtx)`, and
// that length is what decides `unknown type <n>` for a numeric typeuse. A helper whose only purpose
// is a side effect on a length would hide exactly that."
//
// The import section (#8) is the consumer that argument was holding the door open for — an imported
// func's descriptor *is* a type index (encode.ml:203) — so the number is now read as well as
// depended upon. Quoted rather than deleted because the reasoning is the same reasoning that keeps
// `p.name`'s string and `globaltype`'s value: a signature shaped by what the code observably does,
// not by what today's callers happen to use.
func (c *context) inlineFuncType(ft resolvedFunc) uint32 {
	for i, e := range c.typeCtx {
		// **`e.final && len(e.supertypes) == 0`, not just a structural match, as of 0019's own
		// named gap.** An implicit type is always the bare, no-wrapper form — `inline_functype`
		// has no `sub` spelling — so reusing an existing slot that *does* carry a `sub` (final
		// or not, with or without declared supertypes) would collapse two nominally distinct
		// declared types into one index because their comptypes happen to agree structurally,
		// which is exactly the defect this fix exists to stop repeating one layer up.
		//
		// **And `soleMemberOfItsGroup`, which is grave #402** — the third conjunct of the same
		// pattern, read off the same line of the reference and missed twice. See that method.
		if e.kind == compFunc && e.final && len(e.supertypes) == 0 &&
			c.soleMemberOfItsGroup(uint32(i)) && e.ft.equal(ft) {
			return uint32(i)
		}
	}
	c.typeCtx = append(c.typeCtx, resolvedComp{kind: compFunc, ft: ft, final: true})
	c.types.count++
	return uint32(len(c.typeCtx) - 1)
}

// soleMemberOfItsGroup reports whether explicit type index x is the whole of its rec group, which is
// `inline_functype`'s third condition and grave #402.
//
// The reference's predicate is a *pattern on the deftype*, not a structural comparison:
//
//	Lib.List.index_where (function | DefT (RecT [st'], 0l) -> st = st' | _ -> false) c.types.ctx
//
// `RecT [st']` is a one-element list and `0l` is the projection. Three conditions, therefore, and this
// engine had two: the wrapper shape (`SubT (Final, [], _)`, checked above) and the comptype's equality.
// The group's *size* was not checked, so an inline signature could resolve to a type that is a member
// of a multi-member `(rec …)`.
//
// **Group length, not `rec` spelling.** `rectype`'s two arms both call `define_deftype`
// (parser.mly:1288-1298) and a one-member `(rec (type $t (func)))` produces `DefT (RecT [st], 0l)`
// exactly as the bare `(type $t (func))` does — so it is reusable, and a predicate written as "was not
// spelled with `rec`" would be right on the corpus and wrong on the grammar.
//
// Why it matters, and why nothing saw it: an intra-group reference is an *ordinal* under
// `roll_deftypes`, so a member of a two-member group is a different type from a structurally identical
// singleton even though their comptypes compare equal. Reusing one for an inline signature hands out an
// index whose type has different identity — and until `internal/validate` compared rec-group shape
// (`sameDefType`, #351's population), every oracle downstream agreed with itself about it. That is
// grave #349's lesson arriving one production later: **a flat type list cannot express iso-recursive
// equality, and neither can a structural equality that ignores grouping.**
//
// `recExtents` is contiguous, ascending, and covers exactly `[0, len(typeDefs))` — `typeField` records
// one extent per `rectype` field on the way out, deriving its length from how far `typeDefs` advanced —
// which is what licenses "the first extent whose end passes x is x's own". Stated because the scan
// depends on it and the invariant lives in another file.
func (c *context) soleMemberOfItsGroup(x uint32) bool {
	if x >= uint32(len(c.typeDefs)) {
		// The implicit tail: `inlineFuncType` appends each interned signature as its own type and
		// they are in no extent, which `context.recExtents` calls "exactly right — an implicit type
		// has no `rec` spelling to preserve". The reference agrees at the same point, defining one
		// with `DefT (RecT [st], 0l)`: a singleton, hence reusable by the next inline signature.
		return true
	}
	for _, g := range c.recExtents {
		if x < g.start+g.length {
			return g.length == 1
		}
	}
	// Unreachable given the invariant above, and a panic rather than a `true` on `recGroups`'s
	// reason: falling through to "reusable" would silently restore the defect this method exists
	// to stop, and a wrong type index is a well-formed module denoting different types.
	panic(fmt.Sprintf("text: explicit type %d in no rec extent (%d extents, %d types)",
		x, len(c.recExtents), len(c.typeDefs)))
}

// internImplicit is `inline_functype` reached from a parse-time signature: resolve, then intern,
// and report which slot it landed in.
//
// Every caller is a `deferOp`, so this runs in stage 2 where the explicit table is complete —
// which is what makes "reuse a structurally equal existing one" answerable at all. An imported
// func or tag's descriptor **is** a type index (encode.ml:203/191), so the sugar arm `(import "a"
// "b" (func (param i32)))` has to know which slot was produced; see inlineFuncType on why the
// number is part of the signature even where a caller discards it.
//
// # The error-only twin, and why it is gone
//
// A `declareImplicit` stood here as the face for the callers that discard, its comment arguing that
// two functions beat one because "every other caller genuinely discards and turning them all into
// `_, err :=` would spread a fact about one call site across five". The block family (#7) spent the
// last of those five: every remaining caller reads the index, `declareImplicit`'s caller count went
// to zero, and `deadcode` said so. **The premise expired rather than the reasoning being wrong** —
// which is the shape worth recording, because the same paragraph appears verbatim on two sibling
// pairings in this file and one of those (checkExplicit) went the same way in the same PR. A pairing
// justified by a caller majority is a pairing with an expiry date, and the honest close is to delete
// the unused face rather than keep it as documentation of a distribution that no longer holds.
func (c *context) internImplicit(ft funcType) (uint32, error) {
	rf, err := c.resolveFunc(ft)
	if err != nil {
		return 0, err
	}
	return c.inlineFuncType(rf), nil
}

// checkExplicit is the reference's `inline_functype_explicit` (parser.mly:237-247), returning the
// index it resolved.
//
// **The two lookups a typeuse performs are separately timed, and only one of them is skipped.**
// `typeuse` is `LPAR TYPE idx RPAR { fun c -> $3 c type_ }` (:470-471) and `idx`'s VAR arm is
// `lookup c (var $1)` — so the *name* resolution is the typeuse production's own and runs whenever a
// typeuse is written, in every arm. What `ft = ([], [])` defers is the body of
// `inline_functype_explicit`: `func_type c x`, which is the **range** check plus the func-ness check
// plus the comparison. Hence the ordering here — resolveTypeIdx unconditionally, funcTypeAt only when
// there is something to compare.
//
// Fusing the two is a real defect and not a hypothetical: it makes `(func (type $undefined))` legal,
// because the early return would skip the name lookup along with the comparison. The reference's own
// comment at :239 says which one it is deferring — *"type lookup is only triggered when symbolic
// identifiers are used"* — and a symbolic identifier still has to **exist**.
//
// See funcType.isEmpty for the two vectors that pin the deferral of the range check in both
// directions on one module.
//
// The reference's `inline_functype_explicit` **returns `x`** (parser.mly:247, the bare `x` after the
// conditional), and `func_fields`'s inline-import arm spends it — `ExternFuncT (Idx y.it)` at :979.
// So an imported func's descriptor is this index, in both the typeuse and the sugar spelling. Its
// error-only twin `inlineFuncTypeExplicit` was deleted when the block family took its last caller;
// see internImplicit for why that is the premise expiring rather than the reasoning failing.
//
// The index is `r`'s resolved value and is returned **even when the comparison is deferred**, which
// is the reference's shape exactly: the early return skips `func_type c x`, not the identity of the
// type being named. An import whose typeuse is `(type $t)` with no inline signature still imports
// type `$t` — returning 0 on that path would be a silently wrong descriptor on the commonest
// spelling in the corpus, and no error anywhere.
func (c *context) checkExplicit(r idxRef, ft funcType) (uint32, error) {
	idx, err := c.resolveTypeIdx(r)
	if err != nil {
		return 0, err
	}
	if ft.isEmpty() {
		return idx, nil
	}
	want, err := c.funcTypeAt(r.tok, idx)
	if err != nil {
		return 0, err
	}
	got, err := c.resolveFunc(ft)
	if err != nil {
		return 0, err
	}
	if !got.equal(want) {
		return 0, errAt(r.tok, "inline function type does not match explicit type")
	}
	return idx, nil
}

// internBlockImplicit is the block family's sugar arm, which interns *conditionally*
// (parser.mly:746-751, and identically at :770-:776 / :872-:877 / :909-:914), reporting the index
// and — because this arm may not produce one at all — whether there *is* an index.
//
//	match ft with
//	| ([], []) -> ValBlockType None
//	| ([], [t]) -> ValBlockType (Some t)
//	| ft ->  VarBlockType (inline_functype c ft $sloc)
//
// **A block with no params and at most one result gets no type at all** — it encodes as an inline
// blocktype, so nothing is appended and `len(typeCtx)` does not move. `func`'s sugar arm has no such
// case (:970: `inline_functype c' (fst $1 c') loc`, unconditional), and neither does
// `callexpr_type`'s (:846) — so this is the one arm of the family where the two-of-a-kind reading is
// wrong, and it is wrong in the direction that matters: over-interning shifts every later implicit
// type's index, which is what a numeric typeuse's `unknown type <n>` is a function of.
//
// `(block)` and `(block (result i32))` are the overwhelmingly common spellings in the corpus, so a
// version of this that always interned would shift indices in most files that have a block at all.
//
// The third return is the whole reason this is not internImplicit with a condition in front of it.
// The blocktype encoder has to choose between three forms — `0x40`, a valtype byte, and an `s33`
// type index (encode.ml:229-232) — and the condition selecting the third is *exactly* the condition
// under which a type was interned. Two copies of `len(params) == 0 && len(results) <= 1` would be
// two places deciding one fact, and a drift between them is an opener whose immediate names a type
// index the space never grew: a well-formed module denoting a different function, which the suite
// scores green by construction (§9 G-3). So the interner answers both questions in one call and the
// encoder asks rather than re-derives.
//
// It was the third of three error-only/value-returning pairings in this file, copied rather than
// re-derived (*lessons are indexed by shape*) — and **all three lost their error-only face in this
// PR**, to the same cause: the block family took the last discarding caller of each. Three
// simultaneous expiries is the tell that the shape was a phase of the encoder rather than a
// property of the interner. See internImplicit.
func (c *context) internBlockImplicit(ft funcType) (idx uint32, interned bool, err error) {
	if ft.inlineBlockType() {
		return 0, false, nil
	}
	i, err := c.internImplicit(ft)
	return i, true, err
}

// idxRefFromToken builds a idxRef from a heaptype's index token.
//
// A heaptype's `idx` has already been range-checked and decoded by the reader, so this re-derives
// the two arms from the token kind rather than threading a second idxRef through every valtype.
// The NAT arm cannot overflow: `p.idxValue` rejected a 33-bit index as `i32 constant out of range`
// at the production, which is where the reference's `nat32` puts it.
func idxRefFromToken(t Token) idxRef {
	if t.Kind == VarTok {
		return idxRef{tok: t, isVar: true, name: string(t.Value)}
	}
	idx, _ := parseNat(t.Text, 32)
	return idxRef{tok: t, idx: uint32(idx)}
}
