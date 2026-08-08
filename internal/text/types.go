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

// globaltype parses a global type (parser.mly:400-402): a value type, or `(mut valtype)`, and
// returns both halves unresolved.
//
// **It was error-only until #8's import section, and the comment saying so is quoted rather than
// deleted, because the reason it gave was right and the fact it asserted has changed.** It read:
// *"A global's type is never compared against anything — `inline_functype_explicit` is a functype
// comparison — so returning a value here would be designing a representation for a consumer that
// does not exist, which is what 0011 declined for the module type."* Both clauses still hold. What
// arrived is the consumer: `encodeImports` writes `valtype mutability` (encode.ml:194) for an
// imported global, so the value is read by the grammar's own emitter and not by a hypothesis.
//
// The valtype comes back **unresolved**, as a `valType`, because a global's type may name a type
// index that forward-references — the same reason `defineTable` defers, and the same reason the
// whole stage-2 phase exists. `resolveVal` runs in the deferred phase where the table is complete.
func (p *parser) globaltype() (globalType, error) {
	if p.c.at(LParen) && p.c.peek2Keyword(kwMut) {
		if err := p.lpar(kwMut); err != nil {
			return globalType{}, err
		}
		v, err := p.valtype()
		if err != nil {
			return globalType{}, err
		}
		return globalType{val: v, mut: true}, p.rpar()
	}
	v, err := p.valtype()
	return globalType{val: v}, err
}

// storagetype parses a storage type (parser.mly:404-406): a value type or a packed type, and
// returns it unresolved (decision 0021).
//
// **The packed width is read off the token's own spelling, `i8` versus `i16`, the same authority
// `addrtype` already reads a NUMTYPE token's text for** — PACKTYPE is a lexer *class* covering
// both, so the kind alone cannot tell them apart. Following `addrtype`'s own precedent exactly:
// that function rejects any NUMTYPE spelling that is not `i32`/`i64` with `malformed address
// type` rather than defaulting one of the two, because a keyword class is a fact about the
// *lexer* and a class's membership is not this function's to assume stays at two. A spelling
// this function does not recognize is a real defect: it means kinds.go's keyword table grew a
// third PACKTYPE entry with no corresponding case here, which must fail loudly rather than
// silently report `i8`. The value type case returns an unresolved `valType`, for `globaltype`'s
// own reason: a struct or array field may name a type index that forward-references — `(type $a
// (struct (field (ref $b)))) (type $b (struct))` is 0021's own worked example — so resolution
// happens in the deferred phase, not here.
func (p *parser) storagetype() (storageType, error) {
	if p.c.atKeyword(kwPacktype) {
		tok := p.c.next()
		switch tok.Text {
		case "i8":
			return storageType{packed: true, width: 8}, nil
		case "i16":
			return storageType{packed: true, width: 16}, nil
		}
		return storageType{}, errAt(tok, "malformed storage type")
	}
	v, err := p.valtype()
	return storageType{val: v}, err
}

// fieldtype parses a field type (parser.mly:408-410): a storage type, or `(mut storagetype)`, and
// returns it unresolved (decision 0021).
func (p *parser) fieldtype() (fieldType, error) {
	if p.c.at(LParen) && p.c.peek2Keyword(kwMut) {
		if err := p.lpar(kwMut); err != nil {
			return fieldType{}, err
		}
		st, err := p.storagetype()
		if err != nil {
			return fieldType{}, err
		}
		return fieldType{storage: st, mut: true}, p.rpar()
	}
	st, err := p.storagetype()
	return fieldType{storage: st}, err
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

// fieldtypeList parses `fieldtype_list` (parser.mly:412-414), returning each field type it read.
//
// **Returns the list now, as of decision 0021** — the comment that stood here argued a struct's
// fields are never compared, true of `func_type`'s `non-function type` fallthrough and false of
// the wire form: a struct's `CompType.Fields` is retained content, per 0021's option C, and this
// is the production that reads them. `len()` of the returned slice is the count the field index
// space still needs (`anon_fields c x (Lib.List32.length fts)`, parser.mly:420), so no caller
// lost the count by gaining the list.
func (p *parser) fieldtypeList() ([]fieldType, error) {
	var out []fieldType
	for p.atFieldtypeStart() {
		ft, err := p.fieldtype()
		if err != nil {
			return out, err
		}
		out = append(out, ft)
	}
	return out, nil
}

// structtype parses `struct_field_list` (parser.mly:416-425), returning the field list in
// declaration order (decision 0021).
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
// — `x` in the reference is the type index the fields belong to. **The name itself is not
// retained past this function** — 0021 excludes field names from `FieldType` by the wire-form
// authority (`fieldtype`'s production carries no identifier, decode.ml:243-246), the same rule
// `LocalGroup` already applies to a local's name (0016) — so `fields` exists here only to bind
// the per-struct index space and enforce the duplicate check; the appended `fieldType` values are
// what crosses into `compType`.
func (p *parser) structtype() ([]fieldType, error) {
	fields := space{kind: spaceField}
	var out []fieldType
	for p.c.at(LParen) && p.c.peek2Keyword(kwField) {
		if err := p.lpar(kwField); err != nil {
			return nil, err
		}
		if p.c.at(VarTok) {
			tok := p.c.peek()
			name, err := p.bindidx()
			if err != nil {
				return nil, err
			}
			if bindErr := fields.bindAbs(tok, name); bindErr != nil {
				return nil, bindErr
			}
			ft, err := p.fieldtype()
			if err != nil {
				return nil, err
			}
			out = append(out, ft)
		} else {
			fts, err := p.fieldtypeList()
			if err != nil {
				return nil, err
			}
			for range fts {
				fields.bindAnon()
			}
			out = append(out, fts...)
		}
		if err := p.rpar(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// arraytype parses an array type (parser.mly:427-428): one field type (decision 0021).
func (p *parser) arraytype() (fieldType, error) { return p.fieldtype() }

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
// **All three arms' content is returned, as of decision 0021.** `func_type` reaches a struct or
// array as `non-function type` and never looks inside — that fact is still true, and it is why
// `resolveTypeIdx`/`funcTypeAt` never read `compType.fields` — but 0021's consumer is the
// encoder, which writes a struct's or array's fields into the wire form directly rather than
// through `func_type`'s comparison. `kind` replaces the old `isFunc bool`: three states rather
// than two, mirroring `binary.CompKind`'s own three values (already-merged decoder authority,
// `internal/binary/module.go`) — a struct and an array are as distinct from each other as either
// is from a func, and a bool could only ever tell one of those two apart from the third.
func (p *parser) comptype() (compType, error) {
	switch {
	case p.c.at(LParen) && p.c.peek2Keyword(kwStruct):
		if err := p.lpar(kwStruct); err != nil {
			return compType{}, err
		}
		fields, err := p.structtype()
		if err != nil {
			return compType{}, err
		}
		return compType{kind: compStruct, fields: fields}, p.rpar()
	case p.c.at(LParen) && p.c.peek2Keyword(kwArray):
		if err := p.lpar(kwArray); err != nil {
			return compType{}, err
		}
		ft, err := p.arraytype()
		if err != nil {
			return compType{}, err
		}
		// Exactly one field, per arraytype's own arity (decode.ml:257-258) — no vector, unlike a
		// struct's `vec(fieldtype)`, which is why this wraps a single value rather than a list.
		return compType{kind: compArray, fields: []fieldType{ft}}, p.rpar()
	case p.c.at(LParen) && p.c.peek2Keyword(kwFunc):
		if err := p.lpar(kwFunc); err != nil {
			return compType{}, err
		}
		ft, err := p.functype()
		if err != nil {
			return compType{}, err
		}
		return compType{kind: compFunc, ft: ft}, p.rpar()
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

// tabletype parses a table type (parser.mly:460-461): `addrtype limits reftype`, and returns it.
//
// **The retaining version of this function was written once and `unparam` deleted it; this is that
// version, restored by the arrival of the caller it was missing.** The deleted comment's argument
// is preserved because it was correct and is worth reading beside the change: *"both call sites are
// inline-import arms, and an imported table's type belongs to the import section — which this
// emitter does not write … a return value nobody reads is retention built for a hypothetical
// consumer."* The import section is now written (#8), so the same two call sites read the value, and
// the retention grows out of the grammar at a load-bearing spot exactly as 0006 requires.
//
// The *defining* arms still cannot call this, and that has not changed: the sugar branch (`addrtype
// reftype (elem …)`, parser.mly:1205) needs a lookahead *between* `addrtype` and `limits`, so
// `tableField` interleaves the three productions itself. So this function's whole population is
// imports, which is why its return is a `tabType` (element type unresolved) rather than a
// `resolvedTable` — the resolution happens in the deferred phase at the import's own position.
//
// The field order against the binary format's is recorded at `encodeTables`, where the reordering
// happens for a *defined* table: text is `addrtype limits reftype`, binary is `reftype limits`
// (encode.ml:200). The import path reorders identically, through the same `w.valType`/`w.limits`
// pair, because `externtype`'s table arm is `tabletype tt` — one encoder, both populations.
func (p *parser) tabletype() (tabType, error) {
	addr64, err := p.addrtype()
	if err != nil {
		return tabType{}, err
	}
	lim, err := p.limits()
	if err != nil {
		return tabType{}, err
	}
	elem, err := p.reftype()
	if err != nil {
		return tabType{}, err
	}
	return tabType{addr64: addr64, lim: lim, elem: elem}, nil
}

// memorytype parses a memory type (parser.mly:463-464): `addrtype limits`, and returns it.
//
// Value-returning for the reason `tabletype` above records at length: its one caller is an inline
// import, and an imported memory's type is now written by the import section (#8). Nothing here is
// deferred, because a `memorytype` has no name in it to resolve — the same grammar fact
// `defineMemory` states for the defining side.
func (p *parser) memorytype() (memType, error) {
	addr64, err := p.addrtype()
	if err != nil {
		return memType{}, err
	}
	lim, err := p.limits()
	if err != nil {
		return memType{}, err
	}
	return memType{addr64: addr64, lim: lim}, nil
}

// typeuse parses `(type idx)` (parser.mly:470-471) and returns the reference, unresolved.
//
// The reference's arm *is* `lookup c (var …)` — resolution at the production — but it runs inside a
// `fun c ->` whose caller is a stage-2 thunk, after every name is bound. Returning the reference
// and resolving in `runDeferred` reproduces that; resolving here would reject `imports.wast:62`'s
// forward reference, a valid module. See typetable.go's header.
func (p *parser) typeuse() (idxRef, error) {
	if err := p.lpar(kwType); err != nil {
		return idxRef{}, err
	}
	r, err := p.idxValue()
	if err != nil {
		return idxRef{}, err
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
	_, err := p.idxValue()
	return err
}

// idxValue is `idx` returning what it read, for the positions whose index is resolved later.
//
// **It was called `typeIdx`, and the comment that justified that name is quoted here rather than
// deleted, because the name was right for a reason that expired rather than for a wrong reason:**
//
//	Not named `idxValue`, because the *only* index space this stratum resolves is the type space:
//	funcs, tables, labels and locals are `unknown <category>` errors from validation, and #64's own
//	board carries two `unknown label` vectors that are deliberately not this PR's. A helper named
//	for indices generally would invite exactly the scope creep the two-vector deferral avoids.
//
// The export section is what expired it. An export's `externidx` is `lookup c (var …)` against the
// func, table, memory, global or tag space (parser.mly:1258-1263) — the *grammar's* lookup, not
// validation's — and the emitter needs the number it produces, so this stratum now resolves five
// more spaces than the type space and `resolveSpaceIdx` is where they land. What the old paragraph
// got right is the thing it was guarding, and that guard still holds: **a label is still not
// resolved here** and a numeric index is still never looked up, so the scope creep it feared did not
// arrive with the widening. A name naming one space while serving six would be the drifted-scope-note
// defect, which is why the rename came with the consumer rather than before it.
//
// **Both arms of the reference's `idx` are here and they fail differently** (parser.mly:487-489).
// The NAT arm is `nat32 $1` — a width check, no lookup, so a numeric index is never
// `unknown type $name`. The VAR arm is `lookup c (var …)`, the second UTF-8 decode site, and the
// resolution it performs is what idxRef defers.
func (p *parser) idxValue() (idxRef, error) {
	switch {
	case p.c.at(NatTok):
		t := p.c.next()
		n, ok := parseNat(t.Text, 32)
		if !ok {
			return idxRef{}, errAt(t, "i32 constant out of range")
		}
		return idxRef{tok: t, idx: uint32(n)}, nil
	case p.c.at(VarTok):
		t := p.c.peek()
		name, err := decodedVar(p.c.next())
		if err != nil {
			return idxRef{}, err
		}
		return idxRef{tok: t, isVar: true, name: name}, nil
	default:
		return idxRef{}, p.unexpected()
	}
}

// idxList parses `idx_list` (parser.mly:499-501): zero or more indices.
func (p *parser) idxList() error {
	_, err := p.idxListValues()
	return err
}

// idxListValues is idxList returning what it read, for `elemidx_list`.
//
// The recognizer twin above stays because two of `idxList`'s three callers — a subtype's supertype
// list and `br_table`'s labels — read indices this position does not resolve; `idxValue`/`idx`'s
// split, one level up.
func (p *parser) idxListValues() ([]idxRef, error) {
	var refs []idxRef
	for p.c.at(NatTok) || p.c.at(VarTok) {
		r, err := p.idxValue()
		if err != nil {
			return nil, err
		}
		refs = append(refs, r)
	}
	return refs, nil
}

// elemIdxList parses `elemidx_list` (parser.mly:1147-1150) and expands it the way the reference
// does: into one `ref.func x` constant expression per index.
//
// **The expansion is the reference's, transcribed rather than invented**, and it is the load-bearing
// half of `textElem`'s derived-form argument. `elemidx_list` is *not* a second element
// representation — its semantic action is
//
//	let f = function {at; _} as x -> [ref_func x @@@ at] @@@ at in
//	fun c -> List.map f ($1 c func)
//
// so `(elem func $a $b)` and `(elem (ref func) (ref.func $a) (ref.func $b))` build the *same*
// `Elem` node and differ only in the reftype they pair it with. Expanding here means the encoder has
// one list to ask `is_elem_index` about, and the answer for these is true by construction — which is
// how `(elem func …)` reaches flag 0 without the parser remembering that it was spelled with
// `elemkind`.
//
// The indices resolve against the **func** space and may forward-reference, which is why each lands
// in a sink through `retainIdx` rather than being encoded here: `elem.wast:12`'s
// `(elem (i32.const 0) $f)` precedes `(func $f)`.
func (p *parser) elemIdxList() ([]instrSink, error) {
	refs, err := p.idxListValues()
	if err != nil {
		return nil, err
	}
	// The **mode**, not `p.retaining()`, for `intoSink`'s reason (grave #144): this runs at
	// module-field scope, where no enclosing sink exists and the two questions come apart. Asking
	// `retaining()` here returned no elements on a parse that was retaining.
	if !p.retain {
		return nil, nil
	}
	elems := make([]instrSink, 0, len(refs))
	for _, r := range refs {
		s, err := p.elemIdxSink(r)
		if err != nil {
			return nil, err
		}
		elems = append(elems, s)
	}
	return elems, nil
}

// refFuncSpelling is the mnemonic `elemidx_list`'s expansion synthesizes, named once because three
// derivations key on it: the opcode (`opBytes`), the lookup category (`keywords` → `idxLookupKinds`),
// and the writer-side predicate (`elemIdxOf`).
const refFuncSpelling = "ref.func"

// refFuncMnemonic is the synthetic `ref.func` token `elemidx_list`'s expansion needs, with its
// keyword read out of the generated table rather than written here.
//
// **The indirection through `keywords` is the point, not ceremony.** `retainIdx` looks the category
// up by `mnemonic.Keyword`, and a `keywordKind` typed as a literal string would compile whether or
// not it matched what the lexer produces for the same spelling.
//
// The hazard is real and this comment used to describe it wrongly, in a way worth recording because
// the wrong version was the reassuring one. It said an unmatched keyword would resolve indices "in
// whatever space `idxLookupKinds[""]` names, which is `catType`'s zero value and not a refusal."
// Printed: `idxLookupKinds[""]` is **`catNone` (0)**, `catType` is 5, and `idxSpaceFor(catNone)`
// returns **nil** — so an *unrecognized* keyword is refused, loudly, with `cannot yet encode a
// symbolic index on ref.func`. Wrong constant, wrong outcome, and the error was in the direction of
// overstating the danger of the case that is actually safe.
//
// What is genuinely unsafe is the case the old sentence obscured: a keyword that is wrong but **is**
// in `idxLookupKinds` gets a space, and the wrong one. Substituting `TABLE_GET` resolves a func name
// against the table space — `unknown table $f` where the module has no table, and a *valid module
// denoting a different function* where it has one. That is the silent half, and it is what
// TestElemIndexFormNeedsExactlyRefFunc's first row is built around. Asking the table is asking the
// same authority the real `ref.func` token comes from.
func refFuncMnemonic() Token {
	kind, ok := keywords[refFuncSpelling]
	if !ok {
		// Unreachable: `ref.func` is `lexer.mll:327` and the table is generated from it, which
		// TestElemIndexFormNeedsExactlyRefFunc pins from the other end by round-tripping a segment
		// that takes the index form. A missing row here would resolve every `(elem func $f)` index
		// in the wrong space rather than refusing it.
		panic("text: " + refFuncSpelling + " is not in the generated keyword table, so an element " +
			"index list cannot be expanded")
	}
	return Token{Kind: KeywordTok, Keyword: kind, Text: refFuncSpelling}
}

// elemIdxSink builds the one-instruction `ref.func x` expression `elemidx_list` expands an index into.
//
// It goes through `retainIdx` rather than encoding the index directly, because that is the one place
// that knows a func index may forward-reference and must defer — see its comment for why deferring
// only the spaces that need it is a correctness requirement rather than an optimization. So this
// borrows the instruction-building machinery for an instruction the text never spelled, which is
// `sugarZeroOffset`'s situation and the reason that function exists next door; the difference is that
// this one's immediate is not a constant, so it cannot be built without the parser's state.
//
// **The `ref.func` token is synthesized from the generated table, not written as a literal**, and
// which of the two is used matters: `retainIdx` routes by `idxLookupKinds[mnemonic.Keyword]`, so a
// token carrying the wrong keyword — or none — resolves the index in the wrong space or refuses it.
// `refFuncMnemonic` reads the spelling's kind out of `keywords.go`, which is the authority both
// halves of that lookup already derive from; a hand-typed `"REF_FUNC"` would be a third copy of a
// generated fact, and the copy that is wrong is the one nothing checks.
//
// It has no source position, because there is no source text: the reference's expansion is
// `ref_func x` with the *index's* location (`[ref_func x @@@ at]`, parser.mly:1149), and `retainIdx`
// quotes `r.tok` for the errors it raises — which is that same location, and the one a reader needs.
func (p *parser) elemIdxSink(r idxRef) (instrSink, error) {
	op, ok := opBytes(refFuncSpelling)
	if !ok {
		// Unreachable for `elemIdxOf`'s reason, and pinned by the same control: `ref.func` is in
		// the generated opcode table.
		panic("text: ref.func has no opcode, so an element index list cannot be expanded")
	}
	mnemonic := refFuncMnemonic()
	// The save-and-restore around `p.imm` is `plaininstr`'s, copied rather than re-derived: the
	// immediate accumulator is a field, so a nested instruction that did not clear it would append to
	// whatever the enclosing instruction had built. There is no enclosing instruction at a module
	// field, which makes the saved value nil here — the same standing `retainedOffset`'s swap has, and
	// written as a swap for the same reason, so the nil-ness stays a fact about the grammar rather
	// than an assumption.
	saved := p.imm
	p.imm = nil
	defer func() { p.imm = saved }()
	return p.intoSink(func() error {
		if err := p.retainIdx(mnemonic, r); err != nil {
			return err
		}
		p.emit(instr{op: op, imm: p.imm, patch: p.immPatch})
		p.immPatch = nil
		return nil
	})
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
// So the two arms are idxValue's, and only the symbolic one resolves. Delegating to idxValue rather
// than re-reading the token keeps the NAT width check and the UTF-8 decode in one place, and it is
// the *resolution* that differs per position: a label resolves against a relative stack here and
// now, an export's index against an absolute space in stage 2, and a type index against the type
// table. One reader, three resolvers.
//
// **It retains the index, and that it did not was a defect the suite could not see.** `br 0` emitted
// `0x0c` with *no immediate at all* — the body's terminating `0x0b` was then read as the operand, so
// `(func br 0)` decoded as a `br` to label 11 followed by nothing, a well-formed image denoting a
// different function. Every label-taking arm returns before the main switch's `idxRetained` (see
// `immediates`), so the retention had to happen here or nowhere, and nothing failed: the 4162 vectors
// are rejections, and the round-trip table had no `br` row. It was the wabt corpus that said so —
// `token#5`, ours 26 bytes against wabt's 27 — which is the accept-direction control earning its
// keep, exactly as #109 did for the decoder.
//
// The NAT arm retains too, and the numeric case is the one the corpus caught: a written `0` must be
// encoded as a `0`, the absence of a lookup being about *resolution*, not about retention. Reading
// the early return as "nothing to do here" is what produced the bug.
//
// **The symbolic arm was unreachable in build mode until the block family encoded, and it is
// reachable now.** The paragraph here used to record the unreachability, because saying so was what
// kept the symbolic half from reading as tested: a symbolic label needs an enclosing block to bind
// it, and every block form refused at `refuseUnencodable` before its body was parsed — probed over
// all seven spellings (`block`/`loop`/`if`/`try_table`, folded and flat), all seven refusing at the
// block. It also named where the module-level row would belong once a blocktype could be written,
// which is the obligation this discharges: `encodableModules` now carries `(block $l (br $l))` in
// both spellings, asserting `0x0c 00` — a resolution against the wrong depth encodes `br 1`, a
// return, on a module that decodes clean. Re-probed rather than assumed: of the eight
// keyword-by-spelling combinations, six now encode a symbolic label and the two `try_table` ones
// still refuse (`vec catch` has no encoding, #7).
//
// The retention itself remains controlled at `lookupLabel`'s own level too
// (TestLabelIndexCountsAnonymousLevels), which is the level that sees an anonymous block's
// contribution to the count — a thing no single module row distinguishes.
func (p *parser) labelIdx() error {
	depth, err := p.labelIdxValue()
	if err != nil {
		return err
	}
	return p.retainLabelIdx(depth)
}

// labelIdxValue is labelIdx returning the resolved depth instead of retaining it.
//
// The split exists because `br_table` cannot retain its labels as it reads them: the wire form is
// `vec(labelidx) labelidx`, so the count precedes the members and the *last* written label is the
// default rather than a member (see brTable). A reader that appended each depth on sight would be
// committed to an encoding before it knew how many there were.
//
// Both arms are idxValue's, and only the symbolic one resolves — the whole content of labelIdx's
// comment above, which is why the resolution lives here and labelIdx is the retaining wrapper.
func (p *parser) labelIdxValue() (uint32, error) {
	r, err := p.idxValue()
	if err != nil {
		return 0, err
	}
	if !r.isVar {
		// The NAT arm: a width check, already made, and no lookup — but the written index is
		// still the immediate.
		return r.idx, nil
	}
	return p.ctx.labels.lookupLabel(r.tok, r.name)
}

// retainLabelIdx appends a resolved label index to the current instruction's immediates.
//
// Separate from `retainIdx` because a label is not resolved through a `space`: `idxSpaceFor` returns
// nil for `catLabel` by design, so routing labels through the general path would need a special case
// inside it that reads as an exemption rather than as a different mechanism. The encoding is the
// same `u32` either way.
func (p *parser) retainLabelIdx(depth uint32) error {
	if !p.retaining() {
		return nil
	}
	p.appendImm(encodeLocalIdx(depth))
	return nil
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
//
// **It returns the depths rather than retaining them**, because its one caller buffers: `br_table`'s
// encoding needs a count before its members and takes the sequence's *last* element as the default,
// so nothing can be appended until the tail has ended. See brTable for the three transformations.
func (p *parser) labelIdxList() ([]uint32, error) {
	var depths []uint32
	for p.c.at(NatTok) || p.c.at(VarTok) {
		d, err := p.labelIdxValue()
		if err != nil {
			return nil, err
		}
		depths = append(depths, d)
	}
	return depths, nil
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

// bindidxOpt parses `bindidx_opt` (parser.mly:503-505), binds into s, and returns the index bound.
//
// The two arms are `anon c` and `bind c $1`, which is precisely space.bindAnon versus
// space.bindAbs — the split those two methods exist to mirror.
//
// **The index is returned because the reference returns it**, and one caller needs it: `bindidx_opt`
// evaluates to the `x` that `func_fields` and friends receive, and the inline-export arm is
// `$1 (FuncX x) c :: exs` (parser.mly:985-987) — the sugar exports *the field's own index*, which is
// why that spelling performs no lookup. Every other caller discards it, and that asymmetry is the
// reference's too.
//
// **It used to take the category word as a parameter, and the comment justifying that is quoted
// rather than deleted, because it is the defect stated as the rule:**
//
//	category is the word the duplicate message uses, which for funcs is `func` while the
//	ordering message says `function`; see importKind's comment for why that is not a bug.
//
// It is a bug (grave #120). The reference has one word per space: `bind_abs "function" c.funcs`
// (parser.mly:192) *and* `lookup "function" c.funcs` (:157). There is no second vocabulary — the
// `func`/`function` pair the old comment described is `duplicate func` being a **prefix** of the
// reference's `duplicate function $foo` under the harness's substring match, so the three
// `func.wast` vectors passed either way. `data`/`elem` were wrong the same way with no vector at
// all. The word now lives on the space (spaceKind), written once in newContext.
func (p *parser) bindidxOpt(s *space) (uint32, error) {
	// Read before either arm binds: both advance s.count, so reading after would give the *next*
	// index rather than this definition's. The reference has the same ordering — `bind` returns
	// `space.count` before the shift (parser.mly:101-104).
	idx := s.count
	if !p.c.at(VarTok) {
		s.bindAnon()
		return idx, nil
	}
	tok := p.c.peek()
	name, err := p.bindidx()
	if err != nil {
		return 0, err
	}
	if err := s.bindAbs(tok, name); err != nil {
		return 0, err
	}
	return idx, nil
}

// name parses a `name` (parser.mly:339-340): a string token, UTF-8 validated, and returns it.
//
// The **first** UTF-8 decode site, and where 176 of #62's vectors land. Every one of them is a
// string token that lexes cleanly and fails here.
//
// The value is returned because it is the emitter's input (#8): the import section's two names, and
// now the export section's one. Both export spellings retain it, which is what the paragraph
// previously here said they did *not* do — "the export field and the inline-export sugar still
// discard it", honest at the time on 0006's grounds (the section had no emitter, so retaining the
// name would be retention shaped by an absent consumer) and false the moment section 7 landed.
// Superseded rather than deleted, because the discard's honest interval is the part worth keeping:
// it is what the argument for retention looks like *before* the consumer exists.
func (p *parser) name() (string, error) {
	if !p.c.at(StringTok) {
		return "", p.unexpected()
	}
	return decodedName(p.c.next())
}

// stringList parses `string_list` (parser.mly:342-344): zero or more string tokens,
// concatenated.
//
// **No decode.** This is the production that makes `(data "\ef\ff\fe")` legal, and the reason
// a blanket UTF-8 check on string tokens would be wrong about the grammar while passing every
// vector the suite has.
//
// **The concatenation is now returned**, which is the sentence that stood here arriving:
// "the bytes are not accumulated because nothing under 0011 consumes them; when the encoder
// arrives it wants exactly this concatenation." Section 11 is that encoder, and it wants exactly
// this — `Data ($4, …)` is `string_list`'s value verbatim (parser.mly:1096) and `data`'s payload is
// `string bs` (encode.ml:1094). The interval in which the discard was honest is what the quoted
// sentence records: retention grows out of a section's grammar when that section is written (0006),
// and before section 11 there was no grammar asking.
//
// Unconditional rather than gated on `p.retain`, unlike the instruction sink. The bytes come from
// `Token.Value`, which the lexer already unescaped and allocated for every string token in both
// modes — so a mode branch here would save one append per segment and add a value that is nil in
// one mode and not the other, which is a thing to be wrong about for no gain. There is no error
// path, so there is no error return: an always-nil error is a branch no caller can take.
func (p *parser) stringList() []byte {
	var out []byte
	for p.c.at(StringTok) {
		out = append(out, p.c.next().Value...)
	}
	return out
}
