package text

import (
	"fmt"
	"slices"
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
// carrying a live local space per pending operation. It is not done, and the consequence is bounded:
// this stratum resolves no local at all — `unknown local` is validation's and unimplemented — so
// the only reader of a local count is the duplicate check, which compares *written* names. Zero
// board effect either way, declared here rather than left silent, and tracked in #77.

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

// compType is a composite type (parser.mly:446-449). Only the func case carries content: a struct
// or array type is a `non-function type` to every consumer here, and its fields are never compared.
type compType struct {
	isFunc bool
	ft     funcType
}

// typeRef is a `typeuse`'s operand (parser.mly:470-471, `idx` at :487-489), kept unresolved.
//
// **The two arms fail differently and the reference's messages say so.** `idx`'s NAT arm is
// `nat32 $1` with *no lookup* — a number is never `unknown type $name`; it becomes `unknown type
// <n>` only later, from `func_type`'s out-of-range access. The VAR arm is `lookup c (var …)`,
// which raises `unknown type $name` with the name printed. So a numeric index that is out of
// range and a symbolic one that is unbound produce different text, and both are the reference's.
type typeRef struct {
	tok   Token
	isVar bool
	name  string // the decoded identifier, when isVar
	idx   uint32 // the parsed index, when !isVar
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

// resolvedFunc is a functype whose value types are all resolved.
type resolvedFunc struct {
	params  []resolvedVal
	results []resolvedVal
}

func (a resolvedFunc) equal(b resolvedFunc) bool {
	return slices.Equal(a.params, b.params) && slices.Equal(a.results, b.results)
}

// resolvedComp is a compType with its functype resolved.
type resolvedComp struct {
	isFunc bool
	ft     resolvedFunc
}

// defineType records an explicit type definition's content at its index.
//
// Called from `typeDef` as the definition is read, which is the reference's stage 1 — and stage 1
// walks the field list in source order doing nothing else, so recording during the parse is the
// same table. The name was already bound by bindidxOpt, at stage 0.
func (c *context) defineType(ct compType) { c.typeDefs = append(c.typeDefs, ct) }

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
		if !ct.isFunc {
			c.typeCtx = append(c.typeCtx, resolvedComp{})
			continue
		}
		ft, err := c.resolveFunc(ct.ft)
		if err != nil {
			return err
		}
		c.typeCtx = append(c.typeCtx, resolvedComp{isFunc: true, ft: ft})
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
	idx, err := c.resolveTypeIdx(typeRefFromToken(v.heap.tok))
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

// resolveTypeIdx is the reference's `type_ c x` = `lookup "type" c.types.space x`
// (parser.mly:152/:164).
//
// Only the VAR arm can fail here, and the message carries the identifier. It is printed from the
// token's own text rather than reconstructed from the decoded name: the reference's `print` (:145)
// re-quotes a name whose decoded form differs from its spelling, and reproducing that rendering
// from a decoded value is how grave #36 came to quote a byte the input never held.
func (c *context) resolveTypeIdx(r typeRef) (uint32, error) {
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
// **It takes the index rather than the typeRef because the two lookups are separately timed.** The
// name lookup is `typeuse`'s (`$3 c type_`, :471) and happens whenever a typeuse is written; the
// *range* check is this function's and is skipped in the deferred case. Threading a typeRef in here
// and resolving it locally would fuse them, which is the defect inlineFuncTypeExplicit's header
// describes.
func (c *context) funcTypeAt(tok Token, idx uint32) (resolvedFunc, error) {
	if idx >= uint32(len(c.typeCtx)) {
		return resolvedFunc{}, errf(tok, "unknown type %d", idx)
	}
	e := c.typeCtx[idx]
	if !e.isFunc {
		return resolvedFunc{}, errf(tok, "non-function type %d", idx)
	}
	return e.ft, nil
}

// inlineFuncType is the reference's `inline_functype` (parser.mly:222-235): the index of an
// implicit type with this signature, reusing a structurally equal existing one or appending.
//
// The return value is discarded by every caller here, because nothing in a reject-only parser
// reads a type index. It is *not* dropped from the signature, because the number is the whole
// observable effect: appending shifts `len(typeCtx)`, and that length is what decides
// `unknown type <n>` for a numeric typeuse. A helper whose only purpose is a side effect on a
// length would hide exactly that.
func (c *context) inlineFuncType(ft resolvedFunc) uint32 {
	for i, e := range c.typeCtx {
		if e.isFunc && e.ft.equal(ft) {
			return uint32(i)
		}
	}
	c.typeCtx = append(c.typeCtx, resolvedComp{isFunc: true, ft: ft})
	c.types.count++
	return uint32(len(c.typeCtx) - 1)
}

// declareImplicit is `inline_functype` reached from a parse-time signature: resolve, then intern.
//
// Every caller is a `deferOp`, so this runs in stage 2 where the explicit table is complete —
// which is what makes "reuse a structurally equal existing one" answerable at all. The index is
// discarded for the reason inlineFuncType's comment gives; the *length* change is the effect.
func (c *context) declareImplicit(ft funcType) error {
	rf, err := c.resolveFunc(ft)
	if err != nil {
		return err
	}
	c.inlineFuncType(rf)
	return nil
}

// inlineFuncTypeExplicit is the reference's `inline_functype_explicit` (parser.mly:237-247).
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
func (c *context) inlineFuncTypeExplicit(r typeRef, ft funcType) error {
	idx, err := c.resolveTypeIdx(r)
	if err != nil {
		return err
	}
	if ft.isEmpty() {
		return nil
	}
	want, err := c.funcTypeAt(r.tok, idx)
	if err != nil {
		return err
	}
	got, err := c.resolveFunc(ft)
	if err != nil {
		return err
	}
	if !got.equal(want) {
		return errAt(r.tok, "inline function type does not match explicit type")
	}
	return nil
}

// declareBlockImplicit is the block family's sugar arm, which interns *conditionally*
// (parser.mly:746-751, and identically at :770-:776 / :872-:877 / :909-:914).
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
func (c *context) declareBlockImplicit(ft funcType) error {
	if len(ft.params) == 0 && len(ft.results) <= 1 {
		return nil
	}
	return c.declareImplicit(ft)
}

// typeRefFromToken builds a typeRef from a heaptype's index token.
//
// A heaptype's `idx` has already been range-checked and decoded by the reader, so this re-derives
// the two arms from the token kind rather than threading a second typeRef through every valtype.
// The NAT arm cannot overflow: `p.typeIdx` rejected a 33-bit index as `i32 constant out of range`
// at the production, which is where the reference's `nat32` puts it.
func typeRefFromToken(t Token) typeRef {
	if t.Kind == VarTok {
		return typeRef{tok: t, isVar: true, name: string(t.Value)}
	}
	idx, _ := parseNat(t.Text, 32)
	return typeRef{tok: t, idx: uint32(idx)}
}
