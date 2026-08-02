package text

// The type algebra, in the reference's production order (parser.mly:357-473).
//
// Every function here returns only an error, per decision 0011: the parser's job in #62 is to
// say whether the text is well-formed, and nothing constructs a type value. What that costs is
// visible in these bodies — `heaptype` consumes a token and discards which one it was — and
// what it buys is that no type representation gets designed from the requirements of reject
// vectors. When #7's era needs well-formed modules, these productions gain returns or the
// bytes go straight to the encoder (0011's second half); the shape of the recursion does not
// change either way.
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
func (p *parser) heaptype() error {
	switch {
	case p.c.atKeyword(kwAny), p.c.atKeyword(kwNone), p.c.atKeyword(kwEq),
		p.c.atKeyword(kwI31), p.c.atKeyword(kwStruct), p.c.atKeyword(kwArray),
		p.c.atKeyword(kwFunc), p.c.atKeyword(kwNofunc), p.c.atKeyword(kwExn),
		p.c.atKeyword(kwNoexn), p.c.atKeyword(kwExtern), p.c.atKeyword(kwNoextern):
		p.c.next()
		return nil
	default:
		return p.idx()
	}
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
	switch {
	case p.c.atKeyword(kwAny), p.c.atKeyword(kwNone), p.c.atKeyword(kwEq),
		p.c.atKeyword(kwI31), p.c.atKeyword(kwStruct), p.c.atKeyword(kwArray),
		p.c.atKeyword(kwFunc), p.c.atKeyword(kwNofunc), p.c.atKeyword(kwExn),
		p.c.atKeyword(kwNoexn), p.c.atKeyword(kwExtern), p.c.atKeyword(kwNoextern):
		return true
	default:
		return p.c.at(NatTok) || p.c.at(VarTok)
	}
}

// abbreviatedReftypes are the twelve sugar arms of reftype (parser.mly:377-389) — `anyref` for
// `(ref null any)` and its siblings.
//
// A table rather than a switch because the arms differ only in which keyword they match, and
// because the abbreviations are exactly the risk the accept direction runs: an omission here
// rejects a module the spec calls well-formed, and no vector says otherwise.
var abbreviatedReftypes = []keywordKind{
	kwAnyref, kwNullref, kwEqref, kwI31ref, kwStructref, kwArrayref,
	kwFuncref, kwNullfuncref, kwExnref, kwNullexnref, kwExternref, kwNullexternref,
}

// reftype parses a reference type (parser.mly:376-389).
func (p *parser) reftype() error {
	for _, k := range abbreviatedReftypes {
		if p.c.atKeyword(k) {
			p.c.next()
			return nil
		}
	}
	if err := p.lpar(kwRef); err != nil {
		return err
	}
	if p.c.atKeyword(kwNull) { // null_opt, parser.mly:357-359
		p.c.next()
	}
	if err := p.heaptype(); err != nil {
		return err
	}
	return p.rpar()
}

// atReftypeStart reports whether the cursor is at a reference type.
//
// The `(ref` case needs two tokens of lookahead — `(` alone could start `(mut …)` or a
// `(param …)` — which is why the cursor is a slice rather than a stream. peek2 is the only
// place that second token is used and it exists for exactly this production.
func (p *parser) atReftypeStart() bool {
	for _, k := range abbreviatedReftypes {
		if p.c.atKeyword(k) {
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
func (p *parser) valtype() error {
	if p.c.atKeyword(kwNumtype) || p.c.atKeyword(kwVectype) {
		p.c.next()
		return nil
	}
	if !p.atReftypeStart() {
		return p.unexpected()
	}
	return p.reftype()
}

// atValtypeStart reports whether the cursor is at a value type.
func (p *parser) atValtypeStart() bool {
	return p.c.atKeyword(kwNumtype) || p.c.atKeyword(kwVectype) || p.atReftypeStart()
}

// valtypeList parses `list(valtype)` (parser.mly:396-398), returning how many it consumed.
//
// The count is the one piece of information the reject direction genuinely needs from this
// production: `anon_locals c (fst $3)` in func_fields_body (parser.mly:1006) advances the local
// index space by the number of params, and a wrong count shifts every subsequent binding. That
// is a *silent* wrongness under 0011 — no error message mentions an index — so it is pinned by
// test rather than trusted, and it is why this returns an int against 0011's error-only rule.
func (p *parser) valtypeList() (int, error) {
	n := 0
	for p.atValtypeStart() {
		if err := p.valtype(); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// globaltype parses a global type (parser.mly:400-402): a value type, or `(mut valtype)`.
func (p *parser) globaltype() error {
	if p.c.at(LParen) && p.c.peek2Keyword(kwMut) {
		if err := p.lpar(kwMut); err != nil {
			return err
		}
		if err := p.valtype(); err != nil {
			return err
		}
		return p.rpar()
	}
	return p.valtype()
}

// storagetype parses a storage type (parser.mly:404-406): a value type or a packed type.
func (p *parser) storagetype() error {
	if p.c.atKeyword(kwPacktype) {
		p.c.next()
		return nil
	}
	return p.valtype()
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
// space, same silent-index argument as valtypeList's.
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
func (p *parser) functype() error {
	for p.c.at(LParen) && p.c.peek2Keyword(kwParam) {
		if err := p.lpar(kwParam); err != nil {
			return err
		}
		if p.c.at(VarTok) { // sugar: `(param $x valtype)`, exactly one type
			if _, err := p.bindidx(); err != nil {
				return err
			}
			if err := p.valtype(); err != nil {
				return err
			}
		} else if _, err := p.valtypeList(); err != nil {
			return err
		}
		if err := p.rpar(); err != nil {
			return err
		}
	}
	return p.functypeResult()
}

// functypeResult parses `functype_result` (parser.mly:440-444): zero or more `(result …)`.
//
// No named form — `(result $x i32)` is not legal, unlike `(param $x i32)`. The asymmetry is the
// reference's (compare :436 with :443) and is easy to "fix" into a bug.
func (p *parser) functypeResult() error {
	for p.c.at(LParen) && p.c.peek2Keyword(kwResult) {
		if err := p.lpar(kwResult); err != nil {
			return err
		}
		if _, err := p.valtypeList(); err != nil {
			return err
		}
		if err := p.rpar(); err != nil {
			return err
		}
	}
	return nil
}

// comptype parses a composite type (parser.mly:446-449): `(struct …)`, `(array …)`, `(func …)`.
func (p *parser) comptype() error {
	switch {
	case p.c.at(LParen) && p.c.peek2Keyword(kwStruct):
		if err := p.lpar(kwStruct); err != nil {
			return err
		}
		if err := p.structtype(); err != nil {
			return err
		}
		return p.rpar()
	case p.c.at(LParen) && p.c.peek2Keyword(kwArray):
		if err := p.lpar(kwArray); err != nil {
			return err
		}
		if err := p.arraytype(); err != nil {
			return err
		}
		return p.rpar()
	case p.c.at(LParen) && p.c.peek2Keyword(kwFunc):
		if err := p.lpar(kwFunc); err != nil {
			return err
		}
		if err := p.functype(); err != nil {
			return err
		}
		return p.rpar()
	default:
		return p.unexpected()
	}
}

// subtype parses a sub type (parser.mly:451-458): a bare comptype, or `(sub [final] idx* comptype)`.
func (p *parser) subtype() error {
	if !p.c.at(LParen) || !p.c.peek2Keyword(kwSub) {
		return p.comptype()
	}
	if err := p.lpar(kwSub); err != nil {
		return err
	}
	if p.c.atKeyword(kwFinal) {
		p.c.next()
	}
	if err := p.idxList(); err != nil {
		return err
	}
	if err := p.comptype(); err != nil {
		return err
	}
	return p.rpar()
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
func (p *parser) addrtype() error {
	if !p.c.atKeyword(kwNumtype) {
		return nil // empty arm: i32 by default
	}
	tok := p.c.peek()
	if tok.Text != "i32" && tok.Text != "i64" {
		return errAt(tok, "malformed address type")
	}
	p.c.next()
	return nil
}

// limits parses `limits` (parser.mly:466-468): one nat, or two.
func (p *parser) limits() error {
	if !p.c.at(NatTok) {
		return p.unexpected()
	}
	p.c.next()
	if p.c.at(NatTok) {
		p.c.next()
	}
	return nil
}

// tabletype parses a table type (parser.mly:460-461): `addrtype limits reftype`.
func (p *parser) tabletype() error {
	if err := p.addrtype(); err != nil {
		return err
	}
	if err := p.limits(); err != nil {
		return err
	}
	return p.reftype()
}

// memorytype parses a memory type (parser.mly:463-464): `addrtype limits`.
func (p *parser) memorytype() error {
	if err := p.addrtype(); err != nil {
		return err
	}
	return p.limits()
}

// typeuse parses `(type idx)` (parser.mly:470-471).
func (p *parser) typeuse() error {
	if err := p.lpar(kwType); err != nil {
		return err
	}
	if err := p.idx(); err != nil {
		return err
	}
	return p.rpar()
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
func (p *parser) idx() error {
	switch {
	case p.c.at(NatTok):
		t := p.c.next()
		if _, ok := parseNat(t.Text, 32); !ok {
			return errAt(t, "i32 constant out of range")
		}
		return nil
	case p.c.at(VarTok):
		if _, err := decodedVar(p.c.next()); err != nil {
			return err
		}
		return nil
	default:
		return p.unexpected()
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
