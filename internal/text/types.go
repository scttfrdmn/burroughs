package text

// The type algebra, in the reference's production order (parser.mly:357-473).
//
// **The valtype family returns values now (#64), and the rest of the file still returns only an
// error.** The split is not a halfway migration; it is where the reference's own returns are
// needed. `inline_functype_explicit` (parser.mly:245) compares an inline signature against an
// explicit `(type n)` **structurally**, so `functype` and everything it recurses into must yield
// content — mirroring the reference's `fun c -> NumT $1` / `RefT ($1 c)` shapes (:391-394). A
// `globaltype` or `memorytype` is compared with nothing, so it stays error-only.
//
// That is within decision 0011 rather than an exception to it: 0011 governs the parser's *surface*
// — error-only, no module value out of ReadModule — and these values never leave the package. See
// typetable.go's header for the deferred phase that consumes them, and for why resolution cannot
// happen where a type name is used.
//
// The paragraph that stood here said every function returns only an error and that heaptype
// "consumes a token and discards which one it was". That was true for #62 and #63 and is now
// false; it is replaced rather than qualified, because a scope claim describing the previous
// stratum is the drifted-citation defect wearing prose.
//
// Naming follows the reference's productions exactly, so a reader can diff this file against
// parser.mly line by line. Where a production is pure sugar the comment says so and cites the
// arm, because the sugar arms are where an omission is invisible: a sugar form this file
// forgets is a *valid module rejected*, the direction no suite vector covers (contract §9 G-3).

// heaptype parses a heap type (parser.mly:361-374).
//
// Twelve keyword arms plus `idx`. The idx arm is why this is not a simple keyword set: a type
// index is a legal heap type, so `(ref $t)` and `(ref func)` are both well-formed and the
// error for neither is "expected a keyword".
//
// The idx arm's token is returned **unresolved** — `UseHT (Idx ($1 c type_).it)` in the reference
// does resolve it, but that arm runs inside a `fun c ->` the enclosing type definition's stage-1
// thunk invokes, by which time every name is bound. Resolving at the cursor would reject
// `(type $a (struct (field (ref $b)))) (type $b (struct))`, a mutually-recursive pair the GC
// proposal makes legal. See typetable.go's header.
func (p *parser) heaptype() (heapRef, error) {
	for _, k := range absoluteHeaptypes {
		if p.c.atKeyword(k) {
			p.c.next()
			return heapRef{abs: k}, nil
		}
	}
	tok := p.c.peek()
	if err := p.idx(); err != nil {
		return heapRef{}, err
	}
	return heapRef{tok: tok}, nil
}

// absoluteHeaptypes are heaptype's twelve keyword arms (parser.mly:361-372) — every heap type
// that is not a type index.
//
// A table rather than a switch for the same reason abbreviatedReftypes is one, plus a second:
// atHeaptypeStart is a predicate over exactly this set, and two lists would be two places to
// forget an arm. TestHeaptypeStartAgreesWithHeaptype holds them together, derived from the
// keyword table rather than from either list.
var absoluteHeaptypes = []keywordKind{
	kwAny, kwNone, kwEq, kwI31, kwStruct, kwArray,
	kwFunc, kwNofunc, kwExn, kwNoexn, kwExtern, kwNoextern,
}

// atHeaptypeStart reports whether the cursor is at something heaptype can begin with.
//
// Needed because `reftype`'s `(ref …)` arm and the abbreviated arms are distinguished by
// lookahead, and because `tabletype` is `addrtype limits reftype` — a bare `funcref` after two
// nats. Kept as a predicate over the *same* keyword set heaptype consumes, adjacent to it, so
// the two cannot drift apart unnoticed: TestHeaptypeStartAgreesWithHeaptype asserts they
// accept exactly the same tokens, derived by enumerating the table rather than by listing
// these arms again.
func (p *parser) atHeaptypeStart() bool {
	for _, k := range absoluteHeaptypes {
		if p.c.atKeyword(k) {
			return true
		}
	}
	return p.c.at(NatTok) || p.c.at(VarTok)
}

// abbreviatedReftypes are the twelve sugar arms of reftype (parser.mly:377-389) — `anyref` for
// `(ref null any)` and its siblings.
//
// A table rather than a switch because the arms differ only in which keyword they match, and
// because the abbreviations are exactly the risk the accept direction runs: an omission here
// rejects a module the spec calls well-formed, and no vector says otherwise.
// Each abbreviation's expansion is beside it, from the arm's own semantic action: every one is
// `(Null, <heaptype>)`, so all twelve are nullable and they differ only in the heap type. The
// pairing is written out because a wrong expansion is a *comparison* defect now, not just an
// acceptance one — `funcref` versus `(ref func)` decides whether an inline signature matches.
//
// **The pairing is checked now, and it was prose until the emitter existed** —
// TestEveryAbbreviatedReftypeExpandsAsItsTableClaims iterates this table, judging the ten refused
// element types by the heap type their refusal names and `funcref`/`externref` by the 0x70/0x6F
// encode.ml writes. Found by grave #112's sweep: a swapped pairing here used to parse identically
// and no spelling of a test could see it, because every caller of `reftype` discarded the value.
// That is #112's method restated — *a reader that discards cannot be audited by any suite we have*,
// and the repair is to give the value a consumer, not to write a better comment.
var abbreviatedReftypes = []struct {
	kw   keywordKind
	heap keywordKind
}{
	{kwAnyref, kwAny},             // :378  (Null, AnyHT)
	{kwNullref, kwNone},           // :379  (Null, NoneHT)
	{kwEqref, kwEq},               // :380  (Null, EqHT)
	{kwI31ref, kwI31},             // :381  (Null, I31HT)
	{kwStructref, kwStruct},       // :382  (Null, StructHT)
	{kwArrayref, kwArray},         // :383  (Null, ArrayHT)
	{kwFuncref, kwFunc},           // :384  (Null, FuncHT)
	{kwNullfuncref, kwNofunc},     // :385  (Null, NoFuncHT)
	{kwExnref, kwExn},             // :386  (Null, ExnHT)
	{kwNullexnref, kwNoexn},       // :387  (Null, NoExnHT)
	{kwExternref, kwExtern},       // :388  (Null, ExternHT)
	{kwNullexternref, kwNoextern}, // :389  (Null, NoExternHT)
}

// reftype parses a reference type (parser.mly:376-389).
func (p *parser) reftype() (valType, error) {
	for _, a := range abbreviatedReftypes {
		if p.c.atKeyword(a.kw) {
			p.c.next()
			return valType{null: true, heap: heapRef{abs: a.heap}}, nil
		}
	}
	if err := p.lpar(kwRef); err != nil {
		return valType{}, err
	}
	var null bool
	if p.c.atKeyword(kwNull) { // null_opt, parser.mly:357-359
		p.c.next()
		null = true
	}
	h, err := p.heaptype()
	if err != nil {
		return valType{}, err
	}
	if err := p.rpar(); err != nil {
		return valType{}, err
	}
	return valType{null: null, heap: h}, nil
}

// atReftypeStart reports whether the cursor is at a reference type.
//
// The `(ref` case needs two tokens of lookahead — `(` alone could start `(mut …)` or a
// `(param …)` — which is why the cursor is a slice rather than a stream. peek2 is the only
// place that second token is used and it exists for exactly this production.
func (p *parser) atReftypeStart() bool {
	for _, a := range abbreviatedReftypes {
		if p.c.atKeyword(a.kw) {
			return true
		}
	}
	return p.c.at(LParen) && p.c.peek2Keyword(kwRef)
}

// valtype parses a value type (parser.mly:391-394): a number type, a vector type, or a
// reference type.
//
// Written as atKeyword disjunctions rather than a switch on Keyword, and the reason is a lint
// finding worth keeping the answer to: `exhaustive` reads a switch over keywordKind as a claim
// to cover the type's whole domain and lists the 47 missing constants. It is right about the
// shape and wrong about the intent — kinds.go's constants are a *use* of an open vocabulary, not
// an enumeration of it, so no switch here could ever be exhaustive and suppressing the linter
// per-site would be defending the wrong construct. A disjunction says what is meant: these two
// keywords, and everything else falls through.
// **The spelling is the value**, not a proxy for it: the reference's `NumT $1` keeps the lexer's
// NUMTYPE payload, and `i32` versus `f64` is that payload. So `t.Text` here is the same
// information `NumT` carries, and comparing two valTypes compares the same thing the reference's
// structural equality does.
func (p *parser) valtype() (valType, error) {
	if p.c.atKeyword(kwNumtype) || p.c.atKeyword(kwVectype) {
		return valType{num: p.c.next().Text}, nil
	}
	if !p.atReftypeStart() {
		return valType{}, p.unexpected()
	}
	return p.reftype()
}

// atValtypeStart reports whether the cursor is at a value type.
func (p *parser) atValtypeStart() bool {
	return p.c.atKeyword(kwNumtype) || p.c.atKeyword(kwVectype) || p.atReftypeStart()
}

// valtypeList parses `list(valtype)` (parser.mly:396-398), returning the types it consumed.
//
// The reference's arm returns the pair `Lib.List32.length $1, fun c -> List.map …` — a count *and*
// the types — and both halves are read: the count advances the local index space
// (`anon_locals c (fst $3)`, :1007) and the types feed the functype comparison. It used to return
// only the count, which was right while nothing compared anything; `len` of the slice is that
// count, so no caller lost information.
func (p *parser) valtypeList() ([]valType, error) {
	var out []valType
	for p.atValtypeStart() {
		v, err := p.valtype()
		if err != nil {
			return out, err
		}
		out = append(out, v)
	}
	return out, nil
}

// globaltype parses a global type (parser.mly:400-402): a value type, or `(mut valtype)`.
//
// **Error-only, deliberately, and this is the line the value-returning half stops at.** A global's
// type is never compared against anything — `inline_functype_explicit` is a functype comparison —
// so returning a value here would be designing a representation for a consumer that does not
// exist, which is what 0011 declined for the module type. The valtype it reads *does* return one;
// it is discarded here, and that is the honest place for the discard.
func (p *parser) globaltype() error {
	if p.c.at(LParen) && p.c.peek2Keyword(kwMut) {
		if err := p.lpar(kwMut); err != nil {
			return err
		}
		if _, err := p.valtype(); err != nil {
			return err
		}
		return p.rpar()
	}
	_, err := p.valtype()
	return err
}

// storagetype parses a storage type (parser.mly:404-406): a value type or a packed type.
func (p *parser) storagetype() error {
	if p.c.atKeyword(kwPacktype) {
		p.c.next()
		return nil
	}
	_, err := p.valtype()
	return err
}

// fieldtype parses a field type (parser.mly:408-410): a storage type, or `(mut storagetype)`.
func (p *parser) fieldtype() error {
	if p.c.at(LParen) && p.c.peek2Keyword(kwMut) {
		if err := p.lpar(kwMut); err != nil {
			return err
		}
		if err := p.storagetype(); err != nil {
			return err
		}
		return p.rpar()
	}
	return p.storagetype()
}

// atFieldtypeStart reports whether the cursor is at a field type.
func (p *parser) atFieldtypeStart() bool {
	if p.c.atKeyword(kwPacktype) {
		return true
	}
	if p.c.at(LParen) && p.c.peek2Keyword(kwMut) {
		return true
	}
	return p.atValtypeStart()
}

// fieldtypeList parses `fieldtype_list` (parser.mly:412-414), returning the count.
//
// The count feeds `anon_fields c x (Lib.List32.length fts)` (parser.mly:420) — the field index
// space, same silent-index argument valtypeList's count had. It stays a count rather than becoming
// a list because a struct's fields are never compared: `expand_deftype` on a StructT reaches
// `non-function type`, and nothing looks inside.
func (p *parser) fieldtypeList() (int, error) {
	n := 0
	for p.atFieldtypeStart() {
		if err := p.fieldtype(); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// structtype parses `struct_field_list` (parser.mly:416-425).
//
// Two arms, and they differ in whether the fields are named:
//
//	| LPAR FIELD fieldtype_list RPAR struct_field_list   -> anon_fields, count many
//	| LPAR FIELD bindidx fieldtype RPAR struct_field_list -> bind_field, exactly one
//
// The bindidx arm takes exactly one fieldtype, not a list: `(field $x i32 i64)` is *not* legal,
// which is a rejection the grammar makes and no error message announces. It falls out of
// following the arms rather than paraphrasing them.
//
// The field index space is per-struct-type, so it is a local here rather than a context member
// — `x` in the reference is the type index the fields belong to.
func (p *parser) structtype() error {
	var fields space
	for p.c.at(LParen) && p.c.peek2Keyword(kwField) {
		if err := p.lpar(kwField); err != nil {
			return err
		}
		if p.c.at(VarTok) {
			tok := p.c.peek()
			name, err := p.bindidx()
			if err != nil {
				return err
			}
			if err := fields.bindAbs("field", tok, name); err != nil {
				return err
			}
			if err := p.fieldtype(); err != nil {
				return err
			}
		} else {
			n, err := p.fieldtypeList()
			if err != nil {
				return err
			}
			for range n {
				fields.bindAnon()
			}
		}
		if err := p.rpar(); err != nil {
			return err
		}
	}
	return nil
}

// arraytype parses an array type (parser.mly:427-428): one field type.
func (p *parser) arraytype() error { return p.fieldtype() }

// functype parses a function type (parser.mly:430-444).
//
// Params then results, each in two arms — a list form and a named single form. The reference
// splits `functype` from `functype_result` so that a `(param …)` after a `(result …)` cannot
// parse; that ordering is load-bearing and is preserved here as two loops rather than one,
// because a single loop accepting either in any order would admit `(result i32) (param i32)`,
// which the reference rejects and no vector tests.
func (p *parser) functype() (funcType, error) {
	var ft funcType
	for p.c.at(LParen) && p.c.peek2Keyword(kwParam) {
		if err := p.lpar(kwParam); err != nil {
			return ft, err
		}
		if p.c.at(VarTok) { // sugar: `(param $x valtype)`, exactly one type
			if _, err := p.bindidx(); err != nil {
				return ft, err
			}
			v, err := p.valtype()
			if err != nil {
				return ft, err
			}
			ft.params = append(ft.params, v)
		} else {
			vs, err := p.valtypeList()
			if err != nil {
				return ft, err
			}
			ft.params = append(ft.params, vs...)
		}
		if err := p.rpar(); err != nil {
			return ft, err
		}
	}
	rs, err := p.functypeResult()
	ft.results = rs
	return ft, err
}

// functypeResult parses `functype_result` (parser.mly:440-444): zero or more `(result …)`.
//
// No named form — `(result $x i32)` is not legal, unlike `(param $x i32)`. The asymmetry is the
// reference's (compare :436 with :443) and is easy to "fix" into a bug.
//
// Concatenation across repeats, not one list per `(result)`: `(result i32) (result f64)` is the
// two-result signature `snd $3 c @ $5 c` builds, so it must compare equal to `(result i32 f64)`.
// The suite has that spelling at func.wast:50 in the *accept* direction — `(type $sig-4) (param
// i32) (param f64 i32) (result i32)` against a type declared as one `(param i32 f64 i32)` — so a
// per-group representation would reject a valid module and no reject vector would say so.
func (p *parser) functypeResult() ([]valType, error) {
	var out []valType
	for p.c.at(LParen) && p.c.peek2Keyword(kwResult) {
		if err := p.lpar(kwResult); err != nil {
			return out, err
		}
		vs, err := p.valtypeList()
		if err != nil {
			return out, err
		}
		out = append(out, vs...)
		if err := p.rpar(); err != nil {
			return out, err
		}
	}
	return out, nil
}

// comptype parses a composite type (parser.mly:446-449): `(struct …)`, `(array …)`, `(func …)`.
//
// Only the func arm's content is returned. A struct or array reaches `expand_deftype` as a
// `non-function type` and nothing reads its fields, so `isFunc: false` is everything a consumer
// here can use — see funcTypeAt.
func (p *parser) comptype() (compType, error) {
	switch {
	case p.c.at(LParen) && p.c.peek2Keyword(kwStruct):
		if err := p.lpar(kwStruct); err != nil {
			return compType{}, err
		}
		if err := p.structtype(); err != nil {
			return compType{}, err
		}
		return compType{}, p.rpar()
	case p.c.at(LParen) && p.c.peek2Keyword(kwArray):
		if err := p.lpar(kwArray); err != nil {
			return compType{}, err
		}
		if err := p.arraytype(); err != nil {
			return compType{}, err
		}
		return compType{}, p.rpar()
	case p.c.at(LParen) && p.c.peek2Keyword(kwFunc):
		if err := p.lpar(kwFunc); err != nil {
			return compType{}, err
		}
		ft, err := p.functype()
		if err != nil {
			return compType{}, err
		}
		return compType{isFunc: true, ft: ft}, p.rpar()
	default:
		return compType{}, p.unexpected()
	}
}

// subtype parses a sub type (parser.mly:451-458): a bare comptype, or `(sub [final] idx* comptype)`.
//
// The supertype list is read and discarded: `func_type` calls `expand_deftype`, which unrolls to
// the subtype's own comptype (`SubT (_, _, st) -> st`, types.ml:282-284) without consulting the
// parents. So a subtype's inline-signature comparison is against its *own* functype, and
// inheritance is validation's business.
func (p *parser) subtype() (compType, error) {
	if !p.c.at(LParen) || !p.c.peek2Keyword(kwSub) {
		return p.comptype()
	}
	if err := p.lpar(kwSub); err != nil {
		return compType{}, err
	}
	if p.c.atKeyword(kwFinal) {
		p.c.next()
	}
	if err := p.idxList(); err != nil {
		return compType{}, err
	}
	ct, err := p.comptype()
	if err != nil {
		return compType{}, err
	}
	return ct, p.rpar()
}

// addrtype parses the optional address type (parser.mly:346-355).
//
// `%inline`, and its empty arm defaults to i32 — so `(memory 1)` and `(memory i32 1)` are the
// same type. **This production has its own error**: a NUMTYPE that is not i32 or i64 is
// `malformed address type`, which means `(memory f32 1)` is a malformedness the grammar itself
// raises. Distinguishing i32/i64 from f32/f64 needs the token's text, because the generated
// table maps all four to NUMTYPE — the one place in this file where a keyword's *spelling*
// matters rather than its kind, and that is a property of the reference's own lexer collapsing
// them.
// It returns whether the type is I64AT, which the *binary* format spends a flag bit on
// (`flag (at = I64AT) 2` in encode.ml:187) — so this is the second production whose return value
// the encoder needs and the reject-only parser did not.
func (p *parser) addrtype() (bool, error) {
	if !p.c.atKeyword(kwNumtype) {
		return false, nil // empty arm: i32 by default
	}
	tok := p.c.peek()
	if tok.Text != "i32" && tok.Text != "i64" {
		return false, errAt(tok, "malformed address type")
	}
	p.c.next()
	return tok.Text == "i64", nil
}

// limits parses `limits` (parser.mly:466-468): one nat, or two.
//
// **Both nats are `nat64`, and reading them is what closes a missing reject** — grave #112. The
// reference's arms are `{min = nat64 $1 …; max = Some (nat64 $2 …)}`, so a minimum that does not fit
// 64 bits is `i64 constant out of range` *from the parser*. This function previously advanced the
// cursor and read nothing, so `(memory 18446744073709551616)` was accepted — measured before this
// change, and the suite has **no vector** for it: the accept direction §9 G-3 names, found only
// because the encoder needed the value and asking what a nat is worth forced the width question.
// The lesson is indexed by shape: *a reject-only reader that advances past a literal cannot enforce
// the literal's width*, and where the production's action **is** the conversion, skipping the token
// skips the production. The control is `TestLimitsNatsAreCheckedAtSixtyFourBits`, scoped to all four
// call sites because a fix applied only to the two inline ones passes every row about `(memory N)`.
//
// Not a decoder-side check in disguise. `binary.decodeLimits` reads its own u64 budget and reports
// `integer too large`, which is the *binary* grammar's complaint about a LEB; this is the text
// grammar's complaint about a literal, and the two messages are different because the two grammars
// are.
func (p *parser) limits() (limits, error) {
	var lim limits
	if !p.c.at(NatTok) {
		return lim, p.unexpected()
	}
	// Assigned straight into the struct rather than through locals named `min`/`max`: those shadow the
	// builtins, and `bytesIndex`'s section walk in the tests uses `min` two files away.
	var err error
	if lim.min, err = p.nat64(); err != nil {
		return lim, err
	}
	if p.c.at(NatTok) {
		if lim.max, err = p.nat64(); err != nil {
			return lim, err
		}
		lim.hasMax = true
	}
	return lim, nil
}

// nat64 parses one NAT at 64 bits — the reference's `nat64` (parser.mly:43-44).
//
// Its own function beside `nat32` (instr.go) rather than a width parameter on that one, because the
// two carry *different messages* — `i64 constant out of range` against `i32 constant out of range` —
// and a shared helper taking a width would have to take the message too, at which point the two
// callers are the two functions with extra steps. The width and the message are one fact.
//
// This function is grave #112's fix: `limits` used to have no width at all.
func (p *parser) nat64() (uint64, error) {
	t := p.c.next()
	v, ok := parseNat(t.Text, 64)
	if !ok {
		return 0, errAt(t, "i64 constant out of range")
	}
	return v, nil
}

// tabletype parses a table type (parser.mly:460-461): `addrtype limits reftype`, and stays
// **error-only** while every caller discards.
//
// **The retaining version of this function was written, and `unparam` deleted it.** It returned a
// `tabType` that no caller read: both call sites are inline-import arms, and an imported table's type
// belongs to the import section — which this emitter does not write. The *defining* arms cannot call
// it either, because the sugar branch (`addrtype reftype (elem …)`, parser.mly:1205) needs a lookahead
// *between* `addrtype` and `limits`, so `tableField` interleaves the three productions itself. A
// composed `tabletype` therefore has no consumer in either direction, and a return value nobody reads
// is retention built for a hypothetical consumer — the generality-without-a-Go-shaped-consumer
// non-goal, arriving in the smallest possible costume. 0006's rule is that retention grows out of what
// the grammar needs at a *load-bearing* spot; this spot bears nothing.
//
// The field order against the binary format's is recorded at `encodeTables` instead, where the
// reordering actually happens: text is `addrtype limits reftype`, binary is `reftype limits`
// (encode.ml:200).
func (p *parser) tabletype() error {
	if _, err := p.addrtype(); err != nil {
		return err
	}
	if _, err := p.limits(); err != nil {
		return err
	}
	_, err := p.reftype()
	return err
}

// memorytype parses a memory type (parser.mly:463-464): `addrtype limits`. Error-only, for the reason
// `tabletype` above records — its one caller is an inline import, whose type this emitter never writes.
func (p *parser) memorytype() error {
	if _, err := p.addrtype(); err != nil {
		return err
	}
	_, err := p.limits()
	return err
}

// typeuse parses `(type idx)` (parser.mly:470-471) and returns the reference, unresolved.
//
// The reference's arm *is* `lookup c (var …)` — resolution at the production — but it runs inside a
// `fun c ->` whose caller is a stage-2 thunk, after every name is bound. Returning the reference
// and resolving in `runDeferred` reproduces that; resolving here would reject `imports.wast:62`'s
// forward reference, a valid module. See typetable.go's header.
func (p *parser) typeuse() (typeRef, error) {
	if err := p.lpar(kwType); err != nil {
		return typeRef{}, err
	}
	r, err := p.typeIdx()
	if err != nil {
		return typeRef{}, err
	}
	return r, p.rpar()
}

// atTypeuse reports whether the cursor is at `(type …)`.
func (p *parser) atTypeuse() bool { return p.c.at(LParen) && p.c.peek2Keyword(kwType) }

// idx parses an index (parser.mly:487-489): a nat, or a symbolic `$name`.
//
// The VAR arm goes through the `var` helper, which is the **second UTF-8 decode site** and the
// only one reachable from an identifier — see decodedVar. The resolution `lookup c (var …)`
// performs is validation's business, not this stratum's: an index naming an unbound identifier
// is an *unknown* error the reference raises from `lookup`, and #62 does not descend there.
//
// **The NAT arm's width check arrived with #63**, and its absence until then was a real gap
// rather than a deferral: the reference's arm is `nat32 $1 $sloc`, so a 33-bit index is
// `i32 constant out of range` *from the parser*. #62 could not observe it — every idx it reached
// sat in a module field, and no vector puts an over-wide index there without an instruction body
// in the same module. The check is the production's, so it belongs on the production, not on the
// instruction reader that finally made it reachable.
//
// **And the module-field positions are asserted now** — TestModuleFieldIdxIsCheckedAtThirtyTwoBits,
// eleven of them, reject at 2^32 and accept at 2^32-1. The sentence above named an oracle gap and
// then left the property untested for as long as it was true, which is grave #112's class exactly:
// a width the code gets right, a comment saying no vector covers it, and nothing standing in for
// the vector. The instruction and label positions were already covered; these were the ones the
// sentence was about.
func (p *parser) idx() error {
	_, err := p.typeIdx()
	return err
}

// typeIdx is `idx` returning what it read, for the positions whose index is resolved later.
//
// Not named `idxValue`, because the *only* index space this stratum resolves is the type space:
// funcs, tables, labels and locals are `unknown <category>` errors from validation, and #64's own
// board carries two `unknown label` vectors that are deliberately not this PR's. A helper named
// for indices generally would invite exactly the scope creep the two-vector deferral avoids.
//
// **Both arms of the reference's `idx` are here and they fail differently** (parser.mly:487-489).
// The NAT arm is `nat32 $1` — a width check, no lookup, so a numeric index is never
// `unknown type $name`. The VAR arm is `lookup c (var …)`, the second UTF-8 decode site, and the
// resolution it performs is what typeRef defers.
func (p *parser) typeIdx() (typeRef, error) {
	switch {
	case p.c.at(NatTok):
		t := p.c.next()
		n, ok := parseNat(t.Text, 32)
		if !ok {
			return typeRef{}, errAt(t, "i32 constant out of range")
		}
		return typeRef{tok: t, idx: uint32(n)}, nil
	case p.c.at(VarTok):
		t := p.c.peek()
		name, err := decodedVar(p.c.next())
		if err != nil {
			return typeRef{}, err
		}
		return typeRef{tok: t, isVar: true, name: name}, nil
	default:
		return typeRef{}, p.unexpected()
	}
}

// idxList parses `idx_list` (parser.mly:499-501): zero or more indices.
func (p *parser) idxList() error {
	for p.c.at(NatTok) || p.c.at(VarTok) {
		if err := p.idx(); err != nil {
			return err
		}
	}
	return nil
}

// labelIdx parses an `idx` in a label position, resolving it (#80).
//
// The reference's `idx` with `label` as its lookup argument: `lookup c (var $1 $sloc)`. Both arms
// are here and, as everywhere else, **they fail differently** — the NAT arm is `nat32 $1`, a width
// check with *no lookup*, so `(br 1)` in a func with no block is not the parser's error at all. That
// is not a shortcut: all 13 `assert_invalid "unknown label"` vectors are numeric, and reporting them
// here would be the overfitting failure in its purest form — buying pass count by moving
// validation's verdict into the parser, where it would also reject `(br 1)` inside legal code that
// validation accepts.
//
// So the two arms are typeIdx's, and only the symbolic one resolves. Delegating to typeIdx rather
// than re-reading the token keeps the NAT width check and the UTF-8 decode in one place; the name
// `typeIdx` is about which space *defers*, and a label neither defers nor is a type.
func (p *parser) labelIdx() error {
	r, err := p.typeIdx()
	if err != nil {
		return err
	}
	if !r.isVar {
		return nil // the NAT arm: a width check, already made, and no lookup
	}
	return p.ctx.labels.lookupLabel(r.tok, r.name)
}

// labelIdxList parses an `idx_list` whose members are all labels — `br_table`'s tail.
//
// `br_table idx idx_list` resolves **every** member against the label space:
// `Lib.List.split_last ($2 c label :: $3 c label)` (:563-565), the last being the default target.
// Separate from idxList rather than a parameter on it because idxList's other callers are not label
// positions, and a category parameter defaulting to "don't resolve" is the kind of knob that gets
// passed wrongly once and never noticed — over-acceptance being invisible to the suite.
//
// **Both vectors that fall to #80 are `br_table`, and both name the label in the *first* position**
// — so a reader that resolved only the first index would score 2/2 here and silently accept
// `(block $l (br_table $l $nope))`. The control has a row for the tail for exactly that reason.
func (p *parser) labelIdxList() error {
	for p.c.at(NatTok) || p.c.at(VarTok) {
		if err := p.labelIdx(); err != nil {
			return err
		}
	}
	return nil
}

// bindidx parses a binding occurrence of an identifier (parser.mly:507-508) and returns its
// text.
//
// `| VAR { var $1 $sloc }` — the same `var` helper as idx's VAR arm, so this is the same decode
// *site* reached from a different production. Returning the name is not a module value: it is
// the key the duplicate check needs, and `duplicate func $f` prints it.
func (p *parser) bindidx() (string, error) {
	if !p.c.at(VarTok) {
		return "", p.unexpected()
	}
	return decodedVar(p.c.next())
}

// bindidxOpt parses `bindidx_opt` (parser.mly:503-505) and binds into s.
//
// The two arms are `anon c` and `bind c $1`, which is precisely space.bindAnon versus
// space.bindAbs — the split those two methods exist to mirror. category is the word the
// duplicate message uses, which for funcs is `func` while the ordering message says
// `function`; see importKind's comment for why that is not a bug.
func (p *parser) bindidxOpt(s *space, category string) error {
	if !p.c.at(VarTok) {
		s.bindAnon()
		return nil
	}
	tok := p.c.peek()
	name, err := p.bindidx()
	if err != nil {
		return err
	}
	return s.bindAbs(category, tok, name)
}

// name parses a `name` (parser.mly:339-340): a string token, UTF-8 validated.
//
// The **first** UTF-8 decode site, and where 176 of #62's vectors land. Every one of them is a
// string token that lexes cleanly and fails here.
func (p *parser) name() error {
	if !p.c.at(StringTok) {
		return p.unexpected()
	}
	return decodedName(p.c.next())
}

// stringList parses `string_list` (parser.mly:342-344): zero or more string tokens,
// concatenated.
//
// **No decode.** This is the production that makes `(data "\ef\ff\fe")` legal, and the reason
// a blanket UTF-8 check on string tokens would be wrong about the grammar while passing every
// vector the suite has. The bytes are not accumulated because nothing under 0011 consumes them;
// when the encoder arrives it wants exactly this concatenation.
func (p *parser) stringList() error {
	for p.c.at(StringTok) {
		p.c.next()
	}
	return nil
}
