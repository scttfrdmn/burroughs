package text

// The wat parser: recursive descent over the reference's grammar, returning an error and
// nothing else (decision 0011).
//
// **Scope: the whole module grammar, and there is no longer a stratum boundary inside it.** Module
// fields, the type algebra, and instruction bodies — flat (#63) and folded (#64) — all have
// readers. The three strata landed in that order and the sentence that used to stand here said
// "does not descend into instruction bodies", naming a boundary error that had already been
// renamed twice; it is replaced rather than amended, because a scope paragraph describing two of
// three strata is the drifted-citation defect wearing prose.
//
// **And the type-resolution family is now in too**, which retires the rest of that sentence: it
// said `typeuse` resolution and functype equality were "validation's question", and they are not —
// `inline_functype_explicit` (parser.mly:237) is a helper the *grammar* calls, so its three errors
// are `assert_malformed`'s and belong here. Rewritten rather than deleted because the mistake is
// the interesting part: the errors *sound* semantic ("inline function type does not match explicit
// type" is a type mismatch), and the reference's own placement is what settles which layer owns
// them. See typetable.go, which owns the deferred phase and the argument for its shape.
//
// What is genuinely still outside: name resolution for spaces other than types (labels, locals,
// funcs), the two `unknown label` vectors among them.
//
// Which stratum owned which was settled by **defect ownership, not surface form** (Scott's
// ruling on #63's forecast): `expr`/`expr1`'s minimal arm (parser.mly:809/:814) was #63's, since
// it only transports the token stream to a defect in an immediate reader, and #64 owned the
// desugaring *families* — `callexpr_*`/`selectexpr_*` (:836–:865), `if_`/`try_block`
// (:891/:901). `constexpr` (:950) is not sugar despite reaching the same readers: the reference
// marks `expr`/`expr1` `/* Sugar */` and `constexpr` not at all — it is a plain alias for
// `instr_list`.
//
// The error where an instruction was required and absent is `expectedInstr`, and it is a plain
// syntax error in every case. It was `bodyBoundary` and reported `unimplemented` while a later
// stratum was owed; that promise is discharged, and the arm is gone — see expectedInstr for the
// sweep that retired it rather than the impression that it looked finished.
//
// The three checks that live in the grammar rather than in validation — duplicate bindings,
// import ordering, multiple start sections — are context's, and they are the reason a
// return-nothing parser still carries state.

// parser is one parse of one module.
type parser struct {
	c   *cursor
	ctx context
}

// ReadModule reports whether src is a well-formed wat module, to the depth this stratum
// reaches.
//
// The signature is `spec.ReadTextFunc` exactly (decision 0011): error-only, no module value,
// because every vector in #62's bucket is a rejection and designing a module representation
// from reject vectors would fit it to code that never reads it. When well-formed modules are
// needed, 0011's second half applies — the parser emits binary bytes into the proven decoder,
// and `binary.Module` stays the codebase's one module authority.
func ReadModule(src []byte) error {
	c, err := newCursor(src)
	if err != nil {
		return err // a lex error, unwrapped; see newCursor
	}
	p := &parser{c: c}
	if err := p.module(); err != nil {
		return err
	}
	if !p.c.at(EOF) {
		return p.unexpected()
	}
	return nil
}

// module parses `module_` (parser.mly:1389-1392) and the two inline sugar forms.
//
// `(module …)` with an optional `$name`, or a bare field list with no wrapper at all
// (`inline_module`, :1394). The bare form is why this cannot simply demand an LPAR MODULE: a
// .wat file may be a naked sequence of fields.
//
// `(module quote …)` and `(module binary …)` are *not* here. They are the wast script grammar's
// (`module_var` at the script level), already handled by the harness, and a `quote` keyword
// reaching this production is a caller error rather than a malformed module.
func (p *parser) module() error {
	if p.c.at(LParen) && p.c.peek2Keyword(kwModule) {
		if err := p.lpar(kwModule); err != nil {
			return err
		}
		if p.c.at(VarTok) { // module_var (parser.mly:1386), sugar
			if _, err := p.bindidx(); err != nil {
				return err
			}
		}
		if err := p.moduleFields(); err != nil {
			return err
		}
		return p.rpar()
	}
	return p.moduleFields()
}

// moduleFields parses `module_fields` (parser.mly:1308-1382): zero or more fields, in any order.
//
// The reference expresses this as a right-recursive chain of arms, one per field kind, each
// checking its own ordering condition after the fold. Flattened to a loop here, with the
// ordering condition deferred to context — see the curDefined comment block for why the
// flattening has to preserve the reference's evaluation order rather than merely its verdicts.
//
// The ordering error is reported **after** the loop, not inside it: whether a definition
// qualifies depends on what follows it, so no import can be judged when it is read. The other
// two grammar checks (duplicates, multiple start) are immediate, because both are about
// something already seen.
//
// **runDeferred is the same manoeuvre for the type-resolution family and it runs first**, which is
// an ordering claim the reference decides rather than a preference. Both of these errors are
// raised inside `module_fields1`'s arms — `import after <kind>` at :1321 et al, and the two helpers
// through `func`/`tag`/… at :968 et al — and every one of those arms is reached from a *stage-2*
// thunk. `import after` is checked by the arm for the *definition*, after that arm's own `mf ()`
// forced the whole suffix, so the deepest arm raises first; the type-resolution helpers are called
// as their arm's body runs. So on a module with both defects the resolution error is the one the
// reference reports, and this order is what makes that true. No suite vector has both — measured —
// so this is the ours-alone-to-keep-honest half again, and it is a *print* check rather than a
// board one (TestResolutionErrorPrecedesImportOrder).
func (p *parser) moduleFields() error {
	for p.c.at(LParen) {
		if err := p.moduleField(); err != nil {
			return err
		}
	}
	if err := p.ctx.runDeferred(); err != nil {
		return err
	}
	return p.ctx.importOrderErr()
}

// moduleField dispatches one field on its keyword.
//
// The dispatch is on the *second* token, the first being the LPAR the caller already saw. A
// keyword that is not a module field falls through to `unexpected`, which for a bare instruction
// like `(nop)` is the instruction boundary — reported as such rather than as a mystery.
func (p *parser) moduleField() error {
	if !p.c.at(LParen) {
		return p.unexpected()
	}
	kw := p.c.peek2()
	if kw.Kind != KeywordTok {
		return p.unexpectedAt(kw)
	}
	switch kw.Keyword {
	case kwType, kwRec:
		return p.typeField()
	case kwImport:
		return p.importField()
	case kwExport:
		return p.exportField()
	case kwFunc:
		return p.funcField()
	case kwTag:
		return p.tagField()
	case kwGlobal:
		return p.globalField()
	case kwMemory:
		return p.memoryField()
	case kwTable:
		return p.tableField()
	case kwElem:
		return p.elemField()
	case kwData:
		return p.dataField()
	case kwStart:
		return p.startField()
	default:
		return p.unexpectedAt(kw)
	}
}

// typeField parses `type_` → `rectype` (parser.mly:1288-1302): a single `(type …)` or a
// `(rec (type …)*)` group.
//
// The bindidx arm is sugar (`:1279`) and binds into the type space; the anonymous arm still
// occupies an index, which is bindAnon's whole reason for existing.
func (p *parser) typeField() error {
	if p.c.at(LParen) && p.c.peek2Keyword(kwRec) {
		if err := p.lpar(kwRec); err != nil {
			return err
		}
		for p.c.at(LParen) && p.c.peek2Keyword(kwType) {
			if err := p.typeDef(); err != nil {
				return err
			}
		}
		return p.rpar()
	}
	return p.typeDef()
}

// typeDef parses `type_def` (parser.mly:1276-1280).
//
// bindidxOpt binds the *name* — the reference's stage 0 — and defineType records the *content*,
// which is stage 1. Doing both here is sound because stage 1 walks the same field list in the same
// order doing nothing but this, so a single pass over the text produces the identical table. What
// cannot be collapsed into this pass is stage 2, and typetable.go's header has the corpus vector
// that proves it.
func (p *parser) typeDef() error {
	if err := p.lpar(kwType); err != nil {
		return err
	}
	if err := p.bindidxOpt(&p.ctx.types, "type"); err != nil {
		return err
	}
	ct, err := p.subtype()
	if err != nil {
		return err
	}
	p.ctx.defineType(ct)
	return p.rpar()
}

// importField parses `import` (parser.mly:1250-1253): `(import name name externtype)`.
//
// Two `name`s, both UTF-8 decode sites, and this is where most of #62's 176 vectors land — an
// import's module and field names are the commonest place a `\80` appears in the suite.
//
// noteImport is called with the LPAR's token so the recorded position is the import's own start,
// matching `(List.hd m.imports).at`.
func (p *parser) importField() error {
	tok := p.c.peek()
	if err := p.lpar(kwImport); err != nil {
		return err
	}
	p.ctx.noteImport(tok)
	if err := p.name(); err != nil {
		return err
	}
	if err := p.name(); err != nil {
		return err
	}
	if err := p.externtype(); err != nil {
		return err
	}
	return p.rpar()
}

// externtype parses `externtype` (parser.mly:1227-1248).
//
// Six kinds, and two of them have a sugar arm: `(func … typeuse)` versus `(func … functype)`,
// and the same for tag. Each binds into its own index space *before* the type is read, which is
// why an imported func shifts the func index even though it is not a definition — the fact that
// makes a count-based import-ordering check wrong.
func (p *parser) externtype() error {
	if !p.c.at(LParen) {
		return p.unexpected()
	}
	kw := p.c.peek2()
	if kw.Kind != KeywordTok {
		return p.unexpectedAt(kw)
	}
	switch kw.Keyword {
	case kwFunc, kwTag:
		// One arm for two kinds, because the reference's four arms (:1227-:1236 for the typeuse
		// forms, :1234/:1246 for the functype sugar) differ only in which index space the name
		// binds into. The *type* halves are identical, and this is the place a divergence between
		// them would be invisible: an imported tag and an imported func carry the same signature
		// grammar.
		space, category := &p.ctx.funcs, "func"
		if kw.Keyword == kwTag {
			space, category = &p.ctx.tags, "tag"
		}
		if err := p.lpar(kw.Keyword); err != nil {
			return err
		}
		if err := p.bindidxOpt(space, category); err != nil {
			return err
		}
		// **No typeuse arm here takes an inline signature**, so neither helper is reached: the
		// reference's arms are `typeuse` alone or `functype` alone (compare with `func_fields`
		// :963, where both may appear). An `inline_functype` call on the sugar arm would be the
		// reference's (:1236/:1248) and its only effect is the implicit type's index, which
		// nothing here reads — but it *does* move `len(typeCtx)`, so it is recorded rather than
		// skipped. See the deferOp below.
		if p.atTypeuse() {
			if _, err := p.typeuse(); err != nil {
				return err
			}
		} else {
			ft, err := p.functype() // sugar, parser.mly:1234/:1246
			if err != nil {
				return err
			}
			p.ctx.deferOp(func() error { return p.ctx.declareImplicit(ft) })
		}
		return p.rpar()
	case kwGlobal:
		if err := p.lpar(kwGlobal); err != nil {
			return err
		}
		if err := p.bindidxOpt(&p.ctx.globals, "global"); err != nil {
			return err
		}
		if err := p.globaltype(); err != nil {
			return err
		}
		return p.rpar()
	case kwMemory:
		if err := p.lpar(kwMemory); err != nil {
			return err
		}
		if err := p.bindidxOpt(&p.ctx.memories, "memory"); err != nil {
			return err
		}
		if err := p.memorytype(); err != nil {
			return err
		}
		return p.rpar()
	case kwTable:
		if err := p.lpar(kwTable); err != nil {
			return err
		}
		if err := p.bindidxOpt(&p.ctx.tables, "table"); err != nil {
			return err
		}
		if err := p.tabletype(); err != nil {
			return err
		}
		return p.rpar()
	default:
		return p.unexpectedAt(kw)
	}
}

// exportField parses `export` (parser.mly:1265-1267): `(export name externidx)`.
//
// `duplicate export name` is **not** checked here. It is `valid.ml:1146`, the validator's — 26
// suite vectors, correctly outside this stratum, and measured before writing this so the figure
// could not drift into #62's forecast.
func (p *parser) exportField() error {
	if err := p.lpar(kwExport); err != nil {
		return err
	}
	if err := p.name(); err != nil {
		return err
	}
	if err := p.externidx(); err != nil {
		return err
	}
	return p.rpar()
}

// externidx parses `externidx` (parser.mly:1258-1263): `(func idx)` and its four siblings.
func (p *parser) externidx() error {
	if !p.c.at(LParen) {
		return p.unexpected()
	}
	kw := p.c.peek2()
	if kw.Kind != KeywordTok {
		return p.unexpectedAt(kw)
	}
	switch kw.Keyword {
	case kwTag, kwGlobal, kwMemory, kwTable, kwFunc:
		if err := p.lpar(kw.Keyword); err != nil {
			return err
		}
		if err := p.idx(); err != nil {
			return err
		}
		return p.rpar()
	default:
		return p.unexpectedAt(kw)
	}
}

// inlineImport parses `inline_import` (parser.mly:1255-1256): `(import name name)` appearing
// *inside* a definition, which turns the definition into an import.
//
// Returns whether one was consumed, because the caller's grammar branches on it: a `(func
// (import …) …)` takes the func_fields_import arms and never has a body. It also means the field
// does **not** count as a definition for ordering purposes, which is the reference's `funcs <>
// []` being empty on those arms — a subtlety worth stating because "an inline import is still a
// func field" is the plausible wrong reading.
func (p *parser) inlineImport() (bool, error) {
	if !p.c.at(LParen) || !p.c.peek2Keyword(kwImport) {
		return false, nil
	}
	tok := p.c.peek()
	if err := p.lpar(kwImport); err != nil {
		return false, err
	}
	p.ctx.noteImport(tok)
	if err := p.name(); err != nil {
		return false, err
	}
	if err := p.name(); err != nil {
		return false, err
	}
	return true, p.rpar()
}

// inlineExports parses zero or more `inline_export` (parser.mly:1269-1274): `(export name)`
// inside a definition.
//
// A loop because the reference's arm is right-recursive over the whole field
// (`inline_export func_fields`), so `(func (export "a") (export "b"))` is legal. And they come
// *before* an inline import in the recursion, so `(func (export "a") (import "m" "f"))` parses
// while the reverse order does not — an ordering the arms encode and a paraphrase loses.
func (p *parser) inlineExports() error {
	for p.c.at(LParen) && p.c.peek2Keyword(kwExport) {
		if err := p.lpar(kwExport); err != nil {
			return err
		}
		if err := p.name(); err != nil {
			return err
		}
		if err := p.rpar(); err != nil {
			return err
		}
	}
	return nil
}

// funcField parses `func` (parser.mly:959-962) down to its body.
//
// **All four `func_fields` arms reach one of the two type helpers** (:963-:985), and which one is
// decided by whether a `typeuse` was written — so the branch below is the reference's arm choice,
// not a shortcut. With a typeuse it is `inline_functype_explicit` and the inline signature is
// *compared*; without one it is `inline_functype` and the signature *creates* a type. Four of the
// 24 `inline function type` vectors land here (func.wast:600-627).
func (p *parser) funcField() error {
	if err := p.lpar(kwFunc); err != nil {
		return err
	}
	if err := p.bindidxOpt(&p.ctx.funcs, "func"); err != nil {
		return err
	}
	if err := p.inlineExports(); err != nil {
		return err
	}
	imported, impErr := p.inlineImport()
	if impErr != nil {
		return impErr
	}
	use, haveUse := typeRef{}, false
	if p.atTypeuse() {
		u, err := p.typeuse()
		if err != nil {
			return err
		}
		use, haveUse = u, true
	}
	if imported {
		// func_fields_import (parser.mly:991-1001): params and results, no body, no locals.
		ft, err := p.functype()
		if err != nil {
			return err
		}
		p.deferSignature(use, haveUse, ft)
		return p.rpar()
	}
	ft, err := p.funcSignature()
	if err != nil {
		return err
	}
	p.deferSignature(use, haveUse, ft)
	if err := p.locals(); err != nil {
		return err
	}
	p.ctx.markDefined(importFunc)
	// `enter_func` clears the label space (parser.mly:134: `{(enter_let c loc) with labels = empty ()}`)
	// and `func_body` then binds an anonymous one (:1020), so a func's body is itself a label scope
	// with nothing inherited.
	//
	// **Neither line is falsifiable by any wat input today, and both are here anyway.** The first
	// draft of this comment claimed they were, and named the defects: "without the reset a label leaks
	// out of one func into the next (over-acceptance, invisible to the suite); without the anonymous
	// push the depth invariant breaks." Both were probed. Dropping the reset/restore pair for a plain
	// push/pop leaves the board at 4161/2 and `./internal/text/` green; dropping the anonymous push
	// entirely does the same. The reason is structural rather than lucky: every other push site pops
	// under `defer`, so the stack is *already* empty when a func body ends, and a func is a module
	// field with no enclosing label scope to inherit from or leak into. There is no wat text that can
	// tell the three spellings apart — a control asserting otherwise would be one that cannot fail,
	// which is exactly what `labelPushAnon`'s header was rewritten for.
	//
	// So they are kept as **agreement with the reference, stated as such**: two lines transcribed from
	// two cited productions, mirroring a structure whose *point* is that a func body inherits nothing.
	// What makes that honest instead of decorative is that the agreement is machine-checked in the one
	// direction available — `TestLabelStackIsBalancedOnEveryExitPath` pins depth 0 on every exit path
	// including error returns, so the pairing cannot rot silently even though its absence is currently
	// unobservable. The reset becomes load-bearing the moment a label scope can enclose a func (a
	// folded `(func)` inside a block body is not legal wat, but 0011's second half computes indices,
	// and an index read across a func boundary is the defect this forbids). Written down rather than
	// deleted: a line kept for a reason no test can reach is a declared-and-tracked deferral, and the
	// declaration is the part that has to be here.
	saved := p.ctx.labels.labelReset()
	defer p.ctx.labels.labelRestore(saved)
	p.ctx.labels.labelPushAnon()
	// func_body is instr_list (parser.mly:1019), whose empty arm makes `(func)` well-formed.
	if err := p.instrList(); err != nil {
		return err
	}
	return p.rpar()
}

// deferSignature records the stage-2 operation a `typeuse?` plus inline signature implies.
//
// The two helpers are one call site here because the reference's arms pair them that way at every
// one of the ten places they appear: a typeuse present means compare (`inline_functype_explicit`),
// absent means create (`inline_functype`). Writing the pairing once is what stops a site from
// getting the *wrong helper*, which would be a silently missing check rather than a wrong error —
// `inline_functype` never fails, so a site that called it where the reference compares would accept
// every mismatched module and no board would move.
//
// **Recorded in parse order, executed in stage 2.** The order is load-bearing: `inline_functype`
// appends, so it decides the index an implicit type gets and therefore what `unknown type <n>`
// means for a later numeric typeuse. See typetable.go's header for why parse order *is* stage-2
// order.
func (p *parser) deferSignature(use typeRef, haveUse bool, ft funcType) {
	if haveUse {
		p.ctx.deferOp(func() error { return p.ctx.inlineFuncTypeExplicit(use, ft) })
		return
	}
	p.ctx.deferOp(func() error { return p.ctx.declareImplicit(ft) })
}

// funcSignature parses func_fields_body's params and results (parser.mly:1003-1016), binds the
// params into the local index space, and returns the signature.
//
// Params bind as locals — `anon_locals c (fst $3)` at :1006 and `bind_local c $3` at :1009 — so
// `(func (param $x i32) (local $x i32))` is `duplicate local $x`. That is one of #62's 13
// duplicate-binding vectors and it only works if params and locals share one space.
//
// The locals space is reset per function, since it is `enter_func`'s (parser.mly:965).
//
// It was `markFuncSignature` while the binding was its whole purpose; it returns the type now
// because `func_fields`'s arms pass `fst $2 c'` — this signature — to whichever helper applies.
// Renamed rather than given a second name: two functions reading the same production is the drift
// shape, and the *reason* the name changed is that the production always returned this and the
// previous stratum had nothing to do with it.
func (p *parser) funcSignature() (funcType, error) {
	p.ctx.locals = space{}
	var ft funcType
	for p.c.at(LParen) && p.c.peek2Keyword(kwParam) {
		if err := p.lpar(kwParam); err != nil {
			return ft, err
		}
		if p.c.at(VarTok) {
			tok := p.c.peek()
			name, nameErr := p.bindidx()
			if nameErr != nil {
				return ft, nameErr
			}
			if err := p.ctx.locals.bindAbs("local", tok, name); err != nil {
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
			for range vs {
				p.ctx.locals.bindAnon()
			}
		}
		if err := p.rpar(); err != nil {
			return ft, err
		}
	}
	rs, err := p.functypeResult()
	ft.results = rs
	return ft, err
}

// locals parses `func_body`'s local declarations (parser.mly:1023-1029).
//
// Same two-arm shape as params, into the same space, which is what makes a param/local name
// collision a duplicate.
func (p *parser) locals() error {
	for p.c.at(LParen) && p.c.peek2Keyword(kwLocal) {
		if err := p.lpar(kwLocal); err != nil {
			return err
		}
		if p.c.at(VarTok) {
			tok := p.c.peek()
			name, err := p.bindidx()
			if err != nil {
				return err
			}
			if err := p.ctx.locals.bindAbs("local", tok, name); err != nil {
				return err
			}
			// The valtype is read and discarded: a local's type is nothing the grammar
			// compares. Only a functype's value types reach `inline_functype_explicit`.
			if _, err := p.valtype(); err != nil {
				return err
			}
		} else {
			vs, err := p.valtypeList()
			if err != nil {
				return err
			}
			for range vs {
				p.ctx.locals.bindAnon()
			}
		}
		if err := p.rpar(); err != nil {
			return err
		}
	}
	return nil
}

// tagField parses `tag` (parser.mly:1042-1072).
//
// Its four non-export arms (:1047-1067) pair the two helpers exactly as `func_fields` does — a
// typeuse present means `inline_functype_explicit`, absent means `inline_functype` — and unlike
// `func` there is no body arm, so the `functype` is always present in the source. Note the
// signature is read by `functype` in *both* arms, import or not: a tag has no locals, so nothing
// here binds.
func (p *parser) tagField() error {
	if err := p.lpar(kwTag); err != nil {
		return err
	}
	if err := p.bindidxOpt(&p.ctx.tags, "tag"); err != nil {
		return err
	}
	if err := p.inlineExports(); err != nil {
		return err
	}
	imported, err := p.inlineImport()
	if err != nil {
		return err
	}
	use, haveUse := typeRef{}, false
	if p.atTypeuse() {
		use, err = p.typeuse()
		if err != nil {
			return err
		}
		haveUse = true
	}
	ft, err := p.functype()
	if err != nil {
		return err
	}
	p.deferSignature(use, haveUse, ft)
	if !imported {
		p.ctx.markDefined(importTag)
	}
	return p.rpar()
}

// globalField parses `global` (parser.mly:1074-1089).
//
// The non-import arm is `globaltype constexpr`, and constexpr is an instruction sequence — so this
// was the second place the old stratum boundary showed. Both arms now complete: measured, not
// assumed, since a sentence about which of two spellings parses is the kind that goes stale
// silently.
func (p *parser) globalField() error {
	if err := p.lpar(kwGlobal); err != nil {
		return err
	}
	if err := p.bindidxOpt(&p.ctx.globals, "global"); err != nil {
		return err
	}
	if err := p.inlineExports(); err != nil {
		return err
	}
	imported, err := p.inlineImport()
	if err != nil {
		return err
	}
	if err := p.globaltype(); err != nil {
		return err
	}
	if imported {
		return p.rpar()
	}
	p.ctx.markDefined(importGlobal)
	// constexpr is instr_list (parser.mly:951), so `(global i32)` with no initializer is
	// well-formed *grammatically* — the arity is validation's complaint, not the parser's.
	if err := p.instrList(); err != nil {
		return err
	}
	return p.rpar()
}

// memoryField parses `memory` (parser.mly:1112-1134).
//
// Four arms, one of them the `addrtype (data string_list)` sugar that defines a memory sized to
// its own data — and that arm creates a *data segment* too, which is why the reference threads a
// data list out of memory_fields. Under 0011 nothing is created, but the arm still has to parse
// or `(memory (data "abc"))` is a valid module rejected.
func (p *parser) memoryField() error {
	if err := p.lpar(kwMemory); err != nil {
		return err
	}
	if err := p.bindidxOpt(&p.ctx.memories, "memory"); err != nil {
		return err
	}
	if err := p.inlineExports(); err != nil {
		return err
	}
	imported, err := p.inlineImport()
	if err != nil {
		return err
	}
	if imported {
		if err := p.memorytype(); err != nil {
			return err
		}
		return p.rpar()
	}
	// The `addrtype LPAR DATA string_list RPAR` sugar (parser.mly:1129), distinguished from
	// `memorytype` by an LPAR where a nat would be. addrtype is optional in both, so it is
	// consumed first and the branch happens after.
	if err := p.addrtype(); err != nil {
		return err
	}
	if p.c.at(LParen) && p.c.peek2Keyword(kwData) {
		if err := p.lpar(kwData); err != nil {
			return err
		}
		if err := p.stringList(); err != nil {
			return err
		}
		if err := p.rpar(); err != nil {
			return err
		}
		p.ctx.datas.bindAnon() // the sugar's implicit data segment
		p.ctx.markDefined(importMemory)
		return p.rpar()
	}
	if err := p.limits(); err != nil {
		return err
	}
	p.ctx.markDefined(importMemory)
	return p.rpar()
}

// tableField parses `table` (parser.mly:1185-1225).
//
// Five arms. Two are sugar forms taking `(elem …)` and sizing the table to it, and both create
// an elem segment. The `tabletype constexpr1` arm reaches the instruction boundary; the bare
// `tabletype` arm (`:1192`) completes, because the reference synthesizes a `ref.null` init.
func (p *parser) tableField() error {
	if err := p.lpar(kwTable); err != nil {
		return err
	}
	if err := p.bindidxOpt(&p.ctx.tables, "table"); err != nil {
		return err
	}
	if err := p.inlineExports(); err != nil {
		return err
	}
	imported, err := p.inlineImport()
	if err != nil {
		return err
	}
	if imported {
		if err := p.tabletype(); err != nil {
			return err
		}
		return p.rpar()
	}
	if err := p.addrtype(); err != nil {
		return err
	}
	// `addrtype reftype (elem …)` sugar (parser.mly:1205/1216) versus `addrtype limits
	// reftype`: the sugar has no limits, so a reftype where a nat would be is the tell.
	if p.atReftypeStart() {
		// Discarded: a table's element type is never a comparison operand. See types.go's
		// header for where the value-returning half of the type algebra stops.
		if _, err := p.reftype(); err != nil {
			return err
		}
		if err := p.lpar(kwElem); err != nil {
			return err
		}
		p.ctx.elems.bindAnon() // the sugar's implicit elem segment
		// Both sugar arms' contents are instruction-level: elemexpr_list or elemidx_list. The
		// idx list is reachable here; an `(item …)` or folded expr is #63's — the folded arm
		// by the defect-ownership ruling on #63, which put `expr1`'s minimal arm there.
		if p.c.at(NatTok) || p.c.at(VarTok) || p.c.at(RParen) {
			// elemidx_list (parser.mly:1147), whose idx_list has an empty arm — so
			// `(table funcref (elem))` is well-formed.
			if err := p.idxList(); err != nil {
				return err
			}
			if err := p.rpar(); err != nil {
				return err
			}
			p.ctx.markDefined(importTable)
			return p.rpar()
		}
		if err := p.elemexprList(); err != nil { // parser.mly:1205
			return err
		}
		if err := p.rpar(); err != nil {
			return err
		}
		p.ctx.markDefined(importTable)
		return p.rpar()
	}
	if err := p.limits(); err != nil {
		return err
	}
	if _, err := p.reftype(); err != nil {
		return err
	}
	p.ctx.markDefined(importTable)
	if p.c.at(RParen) { // the bare-tabletype arm, parser.mly:1192
		return p.rpar()
	}
	if err := p.constexpr1(); err != nil { // tabletype constexpr1
		return err
	}
	return p.rpar()
}

// dataField parses `data` (parser.mly:1095-1107).
//
// Three arms: passive, `(memory idx) offset string_list`, and the offset-only sugar. The offset
// is an instruction sequence, so only the passive arm completes in this stratum — which is the
// entire reason `(data "\ef\ff\fe")`'s legality is provable here at all.
func (p *parser) dataField() error {
	if err := p.lpar(kwData); err != nil {
		return err
	}
	if err := p.bindidxOpt(&p.ctx.datas, "data"); err != nil {
		return err
	}
	if p.c.at(LParen) && p.c.peek2Keyword(kwMemory) { // memoryuse, parser.mly:1109
		if err := p.lpar(kwMemory); err != nil {
			return err
		}
		if err := p.idx(); err != nil {
			return err
		}
		if err := p.rpar(); err != nil {
			return err
		}
		if err := p.offset(); err != nil {
			return err
		}
		if err := p.stringList(); err != nil {
			return err
		}
		return p.rpar()
	}
	if p.c.at(LParen) {
		// The offset sugar: `(offset …)` or a folded expr (parser.mly:1105).
		if err := p.offset(); err != nil {
			return err
		}
		if err := p.stringList(); err != nil {
			return err
		}
		return p.rpar()
	}
	if err := p.stringList(); err != nil {
		return err
	}
	return p.rpar()
}

// elemField parses `elem` (parser.mly:1158-1180).
//
// Five arms and every non-passive one holds an offset or an elemexpr, so this stratum reaches
// the passive `elemkind elemidx_list` arm — `(elem func $a $b)` — and the declarative arm, and
// stops at the rest.
func (p *parser) elemField() error {
	if err := p.lpar(kwElem); err != nil {
		return err
	}
	if err := p.bindidxOpt(&p.ctx.elems, "elem"); err != nil {
		return err
	}
	if p.c.atKeyword(kwDeclare) {
		p.c.next()
	} else if p.c.at(LParen) && p.c.peek2Keyword(kwTable) { // tableuse, parser.mly:1182
		if err := p.lpar(kwTable); err != nil {
			return err
		}
		if err := p.idx(); err != nil {
			return err
		}
		if err := p.rpar(); err != nil {
			return err
		}
		if err := p.offset(); err != nil { // parser.mly:1164
			return err
		}
		if err := p.elemList(); err != nil {
			return err
		}
		return p.rpar()
	}
	if p.c.at(LParen) && !p.c.peek2Keyword(kwItem) && !p.atReftypeStart() {
		// The offset sugar (parser.mly:1171/:1175): `offset elem_list` or `offset elemidx_list`.
		// An `(item …)` here is an elemexpr in the *passive* arm's elem_list instead, which is
		// what the lookahead separates — the two arms start with the same paren and only the
		// keyword after it decides, so this is peek2 rather than a trial parse.
		//
		// **`atReftypeStart` is the third condition and grave #75 is its absence.** `elem_list`'s
		// second arm is `reftype elemexpr_list` (:1155), and a reftype has a *parenthesized*
		// spelling — `(ref func)`, `(ref null func)`, `(ref $t)` — led by neither `item` nor an
		// instruction. With two conditions this branch claimed `(elem (ref func) (ref.func 0))`'s
		// reftype was an offset and shadowed the arm entirely, rejecting three valid modules
		// (`elem.wast:539`, `:573`, `array.wast:219`). Accept-direction, so no `assert_malformed`
		// could report it; it surfaced when #69 raised the accept oracle from 7 modules to 2130.
		//
		// Three conditions rather than a trial parse because each names a different arm, and the
		// paren alone names none of them: `offset` is `LPAR OFFSET constexpr RPAR | expr`
		// (:1091-1093), so an offset may be *any* folded instruction, and no `(`-based test can
		// separate "an offset written as a bare expr" from "a reftype written as `(ref …)`". What
		// makes the keyword sufficient is that `ref` is not an instruction mnemonic — `ref.func`
		// and `ref.null` are distinct tokens (lexer.mll), so `(ref …)` cannot begin an expr at
		// all, and the three lookaheads partition rather than merely prioritize.
		//
		// **`!peek2Keyword(kwItem)` is measured to discriminate nothing, and is kept anyway —
		// with the measurement, not with an argument.** Falsifying #75's control found this: of
		// the three conditions, deleting the other two each fails named rows, and deleting this
		// one fails none. A `panic()` in the complementary branch (`at(LParen) &&
		// peek2Keyword(kwItem) && !atReftypeStart()`) never fired across the whole suite or the
		// unit tests, and the reason is grammatical rather than accidental: `elemexpr_list` is
		// reachable only *after* a mandatory reftype (parser.mly:1155), so `(item …)` can never
		// be the first thing after `elem`/`bindidx_opt`. Both readings reject
		// `(elem (item (ref.func 0)))`, and a print check says they reject it with the same
		// message. So this is not a check that fires — it is a *statement of which arm an `item`
		// belongs to*, load-bearing only if a future arm makes the position reachable. Named here
		// rather than deleted or left silent, per *unreachability is a grave only when it's
		// silent*; whether to keep it is flagged for Scott in #79's successor rather than decided
		// here, because deleting a condition on the strength of "nothing reaches it today" is the
		// move that the elemexpr arm's own shadowing (#75) should make one cautious about.
		if err := p.offset(); err != nil {
			return err
		}
		// `elemidx_list` (:1147) is the second sugar arm and is just `idx_list`, so a bare index
		// list after the offset is legal and needs no elemkind.
		if p.c.at(NatTok) || p.c.at(VarTok) {
			if err := p.idxList(); err != nil {
				return err
			}
			return p.rpar()
		}
		if err := p.elemList(); err != nil {
			return err
		}
		return p.rpar()
	}
	if err := p.elemList(); err != nil {
		return err
	}
	return p.rpar()
}

// elemList parses `elem_list` (parser.mly:1152-1156): `elemkind elemidx_list`, or
// `reftype elemexpr_list`.
//
// Split out of elemField because four of the five `elem` arms end with one, and inlining it four
// times is how the arms drift apart. The empty case is real — `(elem)` is well-formed, since
// `elemkind` has no empty arm but `reftype elemexpr_list` reaches an empty list.
func (p *parser) elemList() error {
	if p.c.atKeyword(kwFunc) { // elemkind, parser.mly:1136
		p.c.next()
		return p.idxList()
	}
	if p.atReftypeStart() {
		if _, err := p.reftype(); err != nil {
			return err
		}
		return p.elemexprList()
	}
	if p.c.at(RParen) {
		return nil
	}
	return p.expectedInstr()
}

// startField parses `start` (parser.mly:1304-1306) and applies the multiple-start check.
//
// The check is on the LPAR's token, matching the reference's `error x.at` where `x` is the
// Start node spanning the whole field.
func (p *parser) startField() error {
	tok := p.c.peek()
	if err := p.lpar(kwStart); err != nil {
		return err
	}
	if err := p.ctx.checkStart(tok); err != nil {
		return err
	}
	if err := p.idx(); err != nil {
		return err
	}
	return p.rpar()
}

// instrList parses `instr_list` (parser.mly:546-550) as far as this stratum goes, which is: the
// **empty** arm, and no other.
//
// `instr_list` has an empty arm, so `(func)` and `(global i32)` and `(elem funcref)` are all
// well-formed with nothing in them — and that is not a special case to be pattern-matched at each
// call site, it is the production's first arm. Getting this wrong is what the accept-direction
// sweep caught on its first run: fourteen valid modules rejected, every one of them a body that
// was empty rather than unparsed. The distinction the boundary has to draw is *nothing there*
// versus *something we cannot read yet*, and only the second is unimplemented.
//
// Callers whose grammar is `constexpr1` (parser.mly:953 — `instr1 instr_list`, at least one
// instruction) check for the closing paren themselves, because for them an empty sequence is a
// syntax error rather than an empty list.
//
// **#63 makes the flat arm reachable.** `instr_list` is now a loop over `instr1`, and `instr1` is
// `plaininstr | blockinstr | expr` (:552-554) of which this stratum owns the first, the flat form
// of the second, and the minimal case of the third. What was left unread stopped at the boundary as
// before, so the bucket shrank rather than moving — and #64 then read the rest, which is why the
// boundary's `unimplemented` arm no longer exists (see expectedInstr).
//
// **The terminator set is larger than `)` and EOF, and that is a `blockinstr` consequence.** An
// `instr_list` nested in a block ends at the token that closes the *block*, which menhir gets from
// the follow set and a recursive-descent reader has to name: `end`, `else`, and `try_table`'s
// `(catch …)` clauses. Missing them does not accept anything wrong — it reports the *boundary* at
// an `end` this stratum can read perfectly well, which is the unimplemented bucket claiming work
// that is finished. Found by probing `(func block end $l)` after blockinstr landed: the boundary
// moved from `"block"` to `"end"` and the board did not budge, which is the tell that a reader was
// reached and then blocked by its own caller.
func (p *parser) instrList() error {
	for {
		if p.c.at(RParen) || p.c.at(EOF) {
			return nil // the empty arm, parser.mly:547 — reached at the end of every list too
		}
		if p.atBlockTerminator() {
			return nil // the enclosing blockinstr's terminator; its own reader consumes it
		}
		// `instr_list`'s third and fourth arms (:549-550), and they are arms of the *list* rather
		// than of `instr1` because they swallow its tail: `selectinstr_instr_list` (:677) is
		// `SELECT` followed by a `(result …)*` chain that bottoms out in `instr_list` itself, so a
		// flat `select` consumes everything after it. That is why they cannot be arms of instr1 —
		// an `instr1` returns one instruction and these return the whole remainder — and it is why
		// they were missed: `select` and `call_indirect` look like plaininstrs and are not.
		if read, err := p.flatSelectOrCall(); read || err != nil {
			return err
		}
		read, err := p.instr1()
		if err != nil {
			return err
		}
		if !read {
			return p.expectedInstr()
		}
	}
}

// flatSelectOrCall reads `selectinstr_instr_list` (parser.mly:677-680) and `callinstr_instr_list`
// (:689-701): the unfolded `select`, `call_indirect` and `return_call_indirect`, whose type
// annotations are parenthesized even though the instruction is not.
//
// **These are the two `instr_list` arms that are not `instr1`, and nothing read them until #64.**
// `select`, `call_indirect` and `return_call_indirect` are not in `plaininstr` — the reference gives
// each two productions, one folded (`expr1`, :815-:823) and one flat (here), because the flat form's
// `(result …)`/`(param …)` chain would otherwise be ambiguous with a following folded instruction.
// Both were reported as `unimplemented: instruction body at "select"`, which is to say **every
// module containing a flat `select` or `call_indirect` was rejected**: `(func select)`,
// `(func nop select nop)`, `(func call_indirect (type 0))`. Accept-direction, so no
// `assert_malformed` can catch it and the board was silent — found by reading `instr_list`'s arms
// after the folded work, which is *scope controls to the space* applied to a production I had
// treated as three arms because three is what I had implemented.
//
// The chain absorbs the rest of the list, hence the `true` return: the caller's loop must not run
// again, because there is nothing left for it. `(func select nop)` is a select whose `instr_list`
// tail is `nop`, not a select followed by a nop at the same level — a distinction with no
// consequence for a reject-only stratum and a real one once instructions are built.
func (p *parser) flatSelectOrCall() (bool, error) {
	t := p.c.peek()
	if t.Kind != KeywordTok {
		return false, nil
	}
	switch t.Keyword {
	case kwSelect:
		return true, p.selectResults(p.instrList)
	case kwCallIndirect, kwReturnCallIndirect:
		p.c.next() // the keyword
		// The table index is a sugar arm (:693/:699), not an `idx_opt`: a NAT or VAR here is the
		// index, and anything else starts the type chain.
		if p.c.at(NatTok) || p.c.at(VarTok) {
			if err := p.idx(); err != nil {
				return true, err
			}
		}
		// callchain: `callinstr_type_instr_list`'s sugar arm interns unconditionally (:709),
		// as `callexpr_type`'s does.
		return true, p.orderedTypeUse(callchain, p.instrList)
	default:
		// Every other keyword is `instr1`'s, which the caller tries next. Named as a default
		// rather than left to fall through so `exhaustive` sees a real fallback: this switch
		// dispatches two arms out of 173 kinds, it does not enumerate an enum.
		return false, nil
	}
}

// constexpr1 parses `instr1 instr_list` (parser.mly:953): at least one instruction.
//
// The distinction from instrList is the whole reason a closing paren here is a syntax error: for
// these callers an empty sequence is malformed, so `(data (memory 0))` with no offset is malformed
// on the merits. That was the first half of the boundary's ruling and it is now the whole of it —
// expectedInstr reports `unexpected token` for every input, the `unimplemented` half having been
// discharged by the grammar completing.
func (p *parser) constexpr1() error {
	read, err := p.instr1()
	if err != nil {
		return err
	}
	if !read {
		return p.expectedInstr()
	}
	return p.instrList()
}

// offset parses `offset` (parser.mly:1091-1093): `(offset constexpr)` or a folded expr.
//
// The second arm is `expr` — the same sugar arm #63 owns in instr1 — which is why a data segment
// writing `(data (i32.const 0) "abc")` is readable here while `(data (offset …))` needs the
// explicit form. Both land in the same readers.
func (p *parser) offset() error {
	if p.c.at(LParen) && p.c.peek2Keyword(kwOffset) {
		if err := p.lpar(kwOffset); err != nil {
			return err
		}
		// `constexpr` is `instr_list` (:950), *not* `constexpr1` — so `(offset)` is well-formed
		// with an empty sequence. Easy to conflate with the `constexpr1` callers above and the
		// reference is explicit about which each one takes.
		if err := p.instrList(); err != nil {
			return err
		}
		return p.rpar()
	}
	read, err := p.expr()
	if err != nil {
		return err
	}
	if !read {
		return p.expectedInstr()
	}
	return nil
}

// elemexpr parses `elemexpr` (parser.mly:1139-1141): `(item constexpr)` or a folded expr,
// reporting whether it read one.
//
// The bool is what makes `elemexpr_list`'s empty arm (:1144) expressible: an `elem` field's list
// ends at the closing paren, and a `(` that starts something else is #64's rather than an error.
func (p *parser) elemexpr() (bool, error) {
	if !p.c.at(LParen) {
		return false, nil
	}
	if p.c.peek2Keyword(kwItem) {
		if err := p.lpar(kwItem); err != nil {
			return true, err
		}
		if err := p.instrList(); err != nil { // constexpr, :1140
			return true, err
		}
		return true, p.rpar()
	}
	return p.expr()
}

// elemexprList parses `elemexpr_list` (parser.mly:1143-1145): zero or more elemexprs.
func (p *parser) elemexprList() error {
	for {
		read, err := p.elemexpr()
		if err != nil {
			return err
		}
		if !read {
			break
		}
	}
	if !p.c.at(RParen) {
		// A `(` this stratum could not read as an elemexpr: #64's, and reported as the boundary
		// rather than as a syntax error.
		return p.expectedInstr()
	}
	return nil
}

// instr1 parses one instruction (parser.mly:552-554), reporting whether it read one.
//
// Three arms, and #63 owns two and a half of them: `plaininstr`, `blockinstr` in its **flat**
// form, and the `plaininstr expr_list` arm of `expr1` reached through `expr`. `expr1`'s other
// nine arms were #64's, and `expr` now reads all of them — so a false return here means *no*
// instruction starts at the cursor, and the caller's expectedInstr is a syntax error rather than a
// deferral.
//
// The false return must leave the cursor untouched, since expectedInstr reports the token it stops
// on and a half-consumed lookahead would name the wrong one.
func (p *parser) instr1() (bool, error) {
	if read, err := p.plaininstr(); read || err != nil {
		return read, err
	}
	if read, err := p.blockinstr(); read || err != nil {
		return read, err
	}
	return p.expr()
}

// blockinstr parses the flat block family (parser.mly:726-738): `block`, `loop`, `if`/`else` and
// `try_table`, each `KEYWORD labeling_opt … END labeling_end_opt`.
//
// **In scope for #63 by the issue's own Scope list** (`blockinstr` :726 and the block family
// :740-:792, plus `labeling_opt` :510 and `labeling_end_opt` :521), and the seam ruling did not
// move it — the ruling moved `expr1`'s minimal arm *in*, it did not move anything out. The 17
// vectors the forecast called "flat: block/loop/if in folded-free form — no `expr` needed at all"
// are exactly these, and they were still failing as `unimplemented` when the rest of the stratum
// landed. Measured, not assumed: 17 unanswered vectors stop at a boundary whose token is a
// block-family keyword rather than a `(`, in block.wast, loop.wast and if.wast only.
//
// What stays #64's is the *folded* form — `expr1`'s BLOCK/LOOP/IF/TRY_TABLE arms (:826-:834),
// which take `block` without an `END` and get their extent from the closing paren. Same
// keywords, different production, and the difference is real rather than cosmetic: `if_block`
// (:891) and `try_block` (:901) are desugaring families, which is precisely the line the ruling
// drew for #64 — "what the reference itself distinguishes: the desugaring families".
func (p *parser) blockinstr() (bool, error) {
	t := p.c.peek()
	if t.Kind != KeywordTok {
		return false, nil
	}
	switch t.Keyword {
	case kwBlock, kwLoop, kwIf, kwTryTable:
	default:
		return false, nil
	}
	p.c.next()

	label, err := p.labelingOpt()
	if err != nil {
		return true, err
	}
	// `let c' = $2 c $5 in let bt, es = $3 c' in` (:727) — the label is in scope over the *body*
	// and not over the opener's own signature, and the pop is deferred so every error path below
	// leaves the stack as it found it. See labelSpace for why a push/pop pair stands in for the
	// reference's `{c with labels = ...}`.
	//
	// `try_table` is the one arm whose body reads two contexts: the clauses resolve their labels in
	// the *outer* one (`$4 c label`, :797-806) while the body reads `c'`. handlerClauses is what
	// carries that, and the push here is still the body's — see its header.
	p.ctx.pushLabel(label)
	defer p.ctx.labels.labelPop()
	if t.Keyword == kwTryTable {
		if err := p.handlerBlock(); err != nil {
			return true, err
		}
	} else if err := p.block(); err != nil {
		return true, err
	}

	// `if … else …` is a fifth arm rather than an option on the third (:732-:735), and it carries
	// its *own* `labeling_end_opt` — so `if $a else $a end $a` has two end-labels to check, and
	// the reference checks them against the same opener by concatenating them (`$5 @ $8`).
	if t.Keyword == kwIf && p.c.atKeyword(kwElse) {
		p.c.next()
		if err := p.labelingEndOpt(label); err != nil {
			return true, err
		}
		if err := p.instrList(); err != nil {
			return true, err
		}
	}
	if !p.c.atKeyword(kwEnd) {
		// Not the boundary: `END` is a required token of every arm, so its absence is this
		// stratum's own syntax error. `(func block)` is malformed on the merits.
		return true, p.unexpected()
	}
	p.c.next()
	return true, p.labelingEndOpt(label)
}

// atBlockTerminator reports whether the cursor is on a token that ends an enclosing block's
// `instr_list` rather than starting another instruction.
//
// `END` and `ELSE` are `blockinstr`'s own terminators (:728-:738). The `(catch …)` clauses are
// subtler: they are *not* terminators of the body, they *precede* it (`handler_block_body`,
// :792-:806), so an `instr_list` never has one after it — but `handlerBlock` reads the clauses
// before calling instrList, so by then a `(catch …)` cannot legally appear and treating it as a
// stop point would mask a syntax error. Deliberately excluded for that reason; only the two real
// terminators are here.
//
// **The exclusion is necessary and was not sufficient**, which is what the control found. Leaving
// the clauses out is right, and it left a stray `(catch …)` falling through to the boundary to be
// reported as *unimplemented* — a rejection with the wrong layer's name on it. The fix went to the
// boundary, where the false claim was, rather than here, where it would have bought a masked
// syntax error; #70 then derived the clause set from `startsInstruction` rather than listing it,
// and #64 retired the `unimplemented` arm entirely. See expectedInstr for both measurements.
//
// Named as a predicate rather than inlined because it is a *claim about the grammar* — the follow
// set of `instr_list` — and a claim gets a name and a control (TestBlockTerminatorsEndTheList).
func (p *parser) atBlockTerminator() bool {
	return p.c.atKeyword(kwEnd) || p.c.atKeyword(kwElse)
}

// labelingOpt parses `labeling_opt` (parser.mly:510-519) and returns the bound label's *decoded*
// name, or "" for the anonymous arm.
//
// The two arms differ only in whether a `$name` follows, and the label is returned rather than
// checked here because the comparison happens at the matching `end` — which is why this reads as
// a getter and labelingEndOpt carries the check.
//
// **The label is decoded, not read raw, and that is this function's whole content.** The named arm
// is `| bindidx`, and `bindidx` is `| VAR { var $1 $sloc }` (:507-508) — the same `var` helper
// (:48-51) every other binding occurrence goes through, and it decodes:
//
//	let var s loc = ... try ignore (Utf8.decode s); Source.(s @@ r)
//	  with Utf8.Utf8 -> error r "malformed UTF-8 encoding"
//
// So `block $"\ff" end` is malformed for exactly the reason `(func $"\ff")` is (id.wast:31), one
// production away. This read `p.c.next()` and returned the Token, skipping the helper — see the
// grave in labelingEndOpt's header, where the same shortcut cost two more defects.
//
// "" is unambiguously the anonymous arm rather than a name: a VarTok's decoded value is never
// empty, because both spellings reject that at the lexer (`empty identifier`, lexer.mll:817/:819).
// Pinned by TestEmptyIdentifierHasNoSpelling rather than assumed — a cross-layer invariant read
// off another file is a claim.
func (p *parser) labelingOpt() (string, error) {
	if !p.c.at(VarTok) {
		return "", nil
	}
	return decodedVar(p.c.next())
}

// labelingEndOpt parses `labeling_end_opt` (:521-523) and makes the `mismatching label` check.
//
// **The empty opener rejects *any* end-label, which is the arm that is easy to get wrong.** The
// reference's anonymous arm is `List.iter (fun x -> error x.at "mismatching label") xs` (:512-513)
// — an unconditional error over the end-labels, so `(func block end $l)` is a mismatch against
// nothing rather than an unknown label. The named arm compares textually (`x.it <> $1.it`, :518).
// Both are the same message, and 14 of the suite's `mismatching label` vectors are these two arms:
// `block.wast:1484` (`block end $l`, empty opener) and `:1488` (`block $a end $l`, named opener
// disagreeing).
//
// Reported at the *end*-label rather than at the opener, per `error x.at` — the offence is the
// label that does not match, not the block that named one.
//
// **Grave: this pair compared raw lexemes.** `labeling_end_opt` is `| bindidx { [$1] }` and
// `labeling_opt`'s named arm is `| bindidx`, so *both* labels are `var`-decoded (parser.mly:48-51)
// before either is compared — and the comparison the reference makes is `x.it <> $1.it` on the
// decoded strings. Reading `Token.Text` instead skipped the decode and compared the *spelling*,
// which is three defects, not one:
//
//   - `block $"\ff" end` was accepted. The opener never decoded. So were the loop, if and
//     try_table spellings — four arms, one missing call.
//   - `block $"\ff" end $"\ff"` was accepted, because two identical bad spellings compare equal.
//   - `block $a end $"a"` was a `mismatching label`, where the reference makes it a *match*:
//     `$a` and `$"a"` are two spellings of the same name (lexer.mll:815 vs :816), so
//     `Text` differs while `Value` agrees. The mirror, `block $"a" end $a`, likewise.
//
// The first two are accept-direction — no assert_malformed can see a module wrongly *accepted*,
// and the third is the same rejection wearing a wrong reason. What surfaced it was `unparam`
// reporting `labelingOpt`'s error result as always nil: the finding was true, and the reason it
// was true is that the decode which would have made it non-nil had been left out. A dead error
// return as a *missing check wearing a disguise* is grave 0003's shape exactly, and here the
// linter found it one layer earlier than the sweep would have. Sweep for siblings: every other
// `p.c.at(VarTok)` site routes through `bindidx`/`decodedVar` — these two were the only raw
// readers, so the family is closed.
//
// The decode precedes the mismatch check, and that order is the reference's rather than a
// preference: `bindidx` is reduced when its VAR is *read*, so `var`'s decode has already run by
// the time blockinstr's action applies the `labeling_opt` closure that iterates and errors. Hence
// `block $a end $"\ff"` is malformed UTF-8, not a mismatch — the end-label is not well-formed
// enough to disagree. See TestLabelDecodePrecedesComparison.
func (p *parser) labelingEndOpt(label string) error {
	if !p.c.at(VarTok) {
		return nil // the empty arm, which is always legal
	}
	tok := p.c.peek()
	end, err := p.labelingOpt()
	if err != nil {
		return err
	}
	if label != end {
		return errAt(tok, "mismatching label")
	}
	return nil
}

// block parses `block` (parser.mly:740-752): an optional typeuse, then `(param …)`/`(result …)`
// lists, then the instruction sequence.
//
// The three are ordered and each is optional, which `block_param_body` (:754) and
// `block_result_body` (:760) express as right-recursive lists — `(param)` may repeat, then
// `(result)` may repeat, then `instr_list`. A `(param)` *after* a `(result)` is not in the
// grammar, so it falls out of the loops and is reported by instrList's fallthrough.
func (p *parser) block() error {
	return p.blockSignature(p.instrList)
}

// blockSignature reads the part `block` and `handler_block` have in common: `typeuse?` then the
// `(param …)*` and `(result …)*` lists, and **then the body, through the tail it is handed**.
//
// Shared rather than written twice because the reference's two chains are the same shape — compare
// `block_param_body`/`block_result_body` (:754-:764) with
// `handler_block_param_body`/`handler_block_result_body` (:780-:790): four productions differing
// only in the threaded context, which is a semantic-action concern this stratum has no
// representation for. Two copies would be two places to fix the param/result ordering.
//
// **A block's parameter list is NOT `functype`, and delegating to it was a grave (#63).** The first
// draft of this function called `p.functype()`, on the stated reasoning that a block's prefix and a
// functype's are the same production shape. They are not: `functype` has *three* arms (:430-:438)
// and the third is the named sugar `LPAR PARAM bindidx valtype RPAR` (:436), which
// `block_param_body` (:756) does not have — one arm, `LPAR PARAM valtype_list RPAR`, no bindidx.
// So `block (param $x i32) end` is malformed and this reader accepted it.
//
// The suite says so directly, which is the part worth recording: `block.wast:1475`,
// `loop.wast:783` and `if.wast:1513` are `(module quote …)` vectors expecting `unexpected token`
// on exactly this spelling, and three more (`:1479`, `:787`, `if.wast:1517`) expect it on the
// folded form, which is #64's. The vectors existed the whole time; the defect was **stated as the
// rule** in this comment, so a reviewer checking the code against its documentation would have
// found agreement. Caught by #63's own definition of done — *each immediate reader measured against
// the reference production defining its extent, on its own* — which is the discipline finding
// something a shared-prefix argument had talked past. Comments are testimony, and the executable
// grammar outranks them.
//
// `(result …)` has no named form in either chain (compare :762 with `functype_result` :443), so
// that half really is shared and `(result $x i32)` was already rejected.
//
// **The body is a tail parameter and not the caller's business, because the interning order depends
// on it.** The first version took no tail and let each caller read its own body afterwards, which
// put the enclosing block's implicit type in the space *before* the nested one's — measured, index 1
// outer and index 2 inner, for `(func (block (result i32 i32) (block (param i32) drop) unreachable))`.
// The reference is the other way round: every arm reads `let ft, es = $2 c in` and only *then* calls
// its helper (:741-:744, :769-:776, :866-:877, :902-:915), and `$2 c` forces the right-recursive
// chain whose innermost production is the body. So the nested type interns first. No vector sees the
// difference — an index shift is invisible until a numeric typeuse names a shifted index — which is
// exactly why the order is taken from the grammar rather than from the board.
func (p *parser) blockSignature(tail func() error) error {
	return p.orderedTypeUse(blockchain, tail)
}

// signatureKind says how a shared-chain site interns an inline signature when no typeuse was
// written — which is the one thing the ten productions do *not* share.
//
// Two values, and the partition is the reference's: `block`/`if_block`/`try_block`/`handler_block`
// match on `([], [])` and `([], [t])` before reaching `inline_functype` (:746-751 and its three
// copies), while `callexpr_type`/`callinstr_type_instr_list` intern unconditionally (:846/:709).
// Passed as a parameter rather than inferred from the tail, because the tail is about what *follows*
// the signature and this is about what the signature *means* — two facts that happen to correlate
// across today's five families and have no reason to keep doing so.
type signatureKind int

const (
	blockchain signatureKind = iota // conditional: no params and ≤1 result interns nothing
	callchain                       // unconditional, like func's sugar arm
)

// orderedTypeUse reads the chain five production families in the reference share: an optional
// `typeuse`, then `(param …)*`, then `(result …)*`, then a tail.
//
// **The families and their line numbers, because the sharing is a claim about the grammar and not a
// refactor:**
//
//	block_param_body / block_result_body                  :754 :760   tail instr_list
//	handler_block_param_body / handler_block_result_body   :780 :786   tail handler clauses
//	if_block_param_body / if_block_result_body             :879 :885   tail if_
//	callexpr_params / callexpr_results                     :851 :858   tail expr_list
//	callinstr_params_instr_list / callinstr_results_…      :712 :720   tail instr_list
//
// Ten productions, one shape, differing only in the tail and in the threaded context — and the
// context is a semantic-action concern this stratum has no representation for. Writing them
// separately means the param/result *ordering* is decided in five places, and the ordering is
// precisely what the 30 field-ordering vectors test. They land in two files under one reader and
// three under another, so a drift between copies would show on the board in some files and not
// others: the failure mode is a partial red, which reads as a missing feature rather than as a
// disagreement.
//
// **Every caller passes a tail**, which is not an accident of today's five: the tail is the body, and
// the body must be read before the signature's operation is recorded (below). A nil tail would mean a
// site whose interning order is decided by its caller instead of here, and that is the shape the
// measurement caught.
//
// **The `(param)` arm takes a `valtype_list`, with no `bindidx`** (:756), which is the asymmetry
// #63's grave was made of: `functype`'s third arm (:436) *is* the named sugar `LPAR PARAM bindidx
// valtype RPAR`, so delegating here to functype accepted `(block (param $x i32))`. Eight vectors
// turn on it and all eight are folded, so the flat half of the family was the only half the suite
// could see until #64. `(result …)` has no named form in any of the ten (compare :762 with :443),
// so that half really is uniform.
// **The signature's operation is recorded *after* the tail runs**, and that is the reference's
// evaluation order rather than a convenience. Every arm of the family reads `let ft, es = $2 c in`
// and *then* calls a helper: `$2 c` forces the whole right-recursive chain, whose innermost
// production is the body (`instr_list`/`expr_list`/`if_`/`handler_block_body`), so a nested block's
// implicit type is interned before the enclosing block's. The opposite of `func_fields`, where the
// signature is `fst $2 c'` and the body `snd $2 c'`, evaluated in that order (:966-967). Both orders
// are in the grammar and the difference decides the indices, so the tail is called before deferring
// here and after it in funcField.
//
// The first draft of this function had the deferral in the right place and was defeated anyway,
// because `blockSignature` passed a nil tail and its three callers read the body after returning —
// so the sentence above was true of this function and false of the block family it was written for.
// A comment describing an intent the code does not implement, which is the shape the grave-#63 note
// upstairs calls *the defect stated as the rule*: print it, don't reason about it. See
// TestNestedBlockTypeInternsBeforeItsEnclosingOne.
func (p *parser) orderedTypeUse(kind signatureKind, tail func() error) error {
	use, haveUse := typeRef{}, false
	if p.atTypeuse() {
		var err error
		use, err = p.typeuse()
		if err != nil {
			return err
		}
		haveUse = true
	}
	var ft funcType
	// Two loops in this order, never one loop accepting either: the chains are right-recursive and
	// a `(param)` may not follow a `(result)`. One loop taking both would admit a form the
	// reference rejects, which is the accept-direction defect these vectors exist to catch.
	for p.c.at(LParen) && p.c.peek2Keyword(kwParam) {
		if err := p.lpar(kwParam); err != nil {
			return err
		}
		vs, err := p.valtypeList()
		if err != nil {
			return err
		}
		ft.params = append(ft.params, vs...)
		if err := p.rpar(); err != nil {
			return err
		}
	}
	rs, err := p.functypeResult()
	if err != nil {
		return err
	}
	ft.results = rs
	if err := tail(); err != nil {
		return err
	}
	p.deferBlockSignature(kind, use, haveUse, ft)
	return nil
}

// deferBlockSignature is deferSignature for the shared chain, differing only in the sugar arm's
// conditional interning. See signatureKind, and declareBlockImplicit for why the condition exists.
func (p *parser) deferBlockSignature(kind signatureKind, use typeRef, haveUse bool, ft funcType) {
	switch {
	case haveUse:
		p.ctx.deferOp(func() error { return p.ctx.inlineFuncTypeExplicit(use, ft) })
	case kind == blockchain:
		p.ctx.deferOp(func() error { return p.ctx.declareBlockImplicit(ft) })
	default:
		p.ctx.deferOp(func() error { return p.ctx.declareImplicit(ft) })
	}
}

// handlerBlock parses `handler_block` (:853-:864) — `try_table`'s block, whose body is preceded by
// zero or more `(catch …)` clauses.
//
// Four handler arms (:874-:806), and all four are read even though no vector reaches the last
// two: a handler set missing an arm rejects a legal module, and that is the accept-direction class
// decision 0007 says the suite cannot falsify.
func (p *parser) handlerBlock() error {
	// The clause reader is shared with the folded arm (#64) — see handlerClauses. It was inline
	// here while there was one caller; the folded `try_table` is the second, and two copies of a
	// four-arm set is two places to lose an arm. It is passed *in* rather than called after, because
	// `handler_block_body` is the innermost production of the chain `$2 c` forces (:792-:806) — see
	// blockSignature for what depends on that.
	return p.blockSignature(p.handlerClauses)
}

// foldedBlock parses `expr1`'s four block arms (parser.mly:826-834): `BLOCK`/`LOOP` over `block`,
// `IF` over `if_block`, `TRY_TABLE` over `try_block`. The leader is unconsumed on entry.
//
// **The signature half is the flat family's reader, called rather than copied.** `if_block`
// (:865-:877) and `try_block` (:901-:915) have the same shape as `block` (:740) — an optional
// `typeuse`, then `(param …)*`, then `(result …)*` — differing only in what terminates the chain:
// `block_result_body` bottoms out in `instr_list` (:761), `if_block_result_body` in `if_` (:886),
// `try_block_result_body` in `try_block_handler_body` (:923). So the ordered param/result chain is
// one production wearing three names, and `blockSignature` is it.
//
// That reuse is load-bearing rather than tidy. `block_param_body` takes `LPAR PARAM valtype_list
// RPAR` (:756) — a `valtype_list`, with **no** `bindidx` — and a second folded implementation is a
// second place for `(block (param $x i32))` to be wrongly admitted. It was wrongly admitted once,
// by delegating to `functype`, whose third arm *is* the named sugar (grave, #63). The eight vectors
// that turn on it are all folded, so until now the suite could only see the flat half.
//
// **No `END`, therefore no `labeling_end_opt`.** Extent comes from the closing paren, so a folded
// block cannot mismatch its own end label — it has none. That is why this takes the label and
// discards it where `blockinstr` threads it through: the binding still happens (a `$l` here scopes
// over the body for `br`), and the *comparison* has no second operand.
func (p *parser) foldedBlock(leader keywordKind) error {
	p.c.next() // the keyword
	label, err := p.labelingOpt()
	if err != nil {
		return err
	}
	// The binding happens here even though the *comparison* has no second operand: a folded
	// `(block $l …)` scopes `$l` over its body exactly as the flat form does, and the label was
	// discarded before #80 because nothing consulted it. `(block $l (br $l))` folded is legal, and
	// a reader that skipped the push would reject it — accept-direction, so no vector reports it.
	p.ctx.pushLabel(label)
	defer p.ctx.labels.labelPop()
	var body func() error
	switch leader {
	case kwIf:
		body = p.ifBody
	case kwTryTable:
		body = p.handlerClauses
	default:
		// `block` and `loop`, whose tail is a plain `instr_list` (:741/:748). Unreachable for
		// anything else — expr1 dispatched here on exactly these four leaders — and a default
		// rather than a fifth case because the caller owns the domain.
		body = p.instrList
	}
	return p.blockSignature(body)
}

// ifBody parses `if_` (parser.mly:891-898), the folded `if`'s tail: any number of folded operands
// pushing the condition, then `(then …)` and optionally `(else …)`.
//
// **`(then …)` is mandatory and that is the whole reason this is not `instrList`.** The production's
// two sugar arms both require it (:893/:896), and its first arm `expr if_` (:891) is right-recursive
// over operands — so `(if (then))` is legal, `(if)` is not, and `(if i32.const 0 (then))` is not
// either, because a bare `i32.const` outside a fold is not an `expr`. That last spelling is
// if.wast:1561, which the suite has and the flat reader could never be asked about.
//
// The operand loop must stop at `(then`, not consume it: `then` is a keyword that starts no
// instruction, so `expr` declines it and leaves the paren — the same peek2-without-consuming
// contract that lets `(func (param i32))` survive.
func (p *parser) ifBody() error {
	for {
		read, err := p.expr()
		if err != nil {
			return err
		}
		if !read {
			break
		}
	}
	if err := p.lpar(kwThen); err != nil {
		return err
	}
	if err := p.instrList(); err != nil {
		return err
	}
	if err := p.rpar(); err != nil {
		return err
	}
	if !p.c.at(LParen) || !p.c.peek2Keyword(kwElse) {
		return nil // the one-armed sugar arm, :896
	}
	if err := p.lpar(kwElse); err != nil {
		return err
	}
	if err := p.instrList(); err != nil {
		return err
	}
	return p.rpar()
}

// callexprType parses `callexpr_type` (parser.mly:842-848) and the two chains under it,
// `callexpr_params` (:851) and `callexpr_results` (:858).
//
// Structurally the same ordered chain as `blockSignature` — optional `typeuse`, then `(param …)*`,
// then `(result …)*` — and *not* shared with it, because the chains bottom out differently:
// `callexpr_results` ends in `expr_list` (:860), the folded operands, where `block_result_body` ends
// in `instr_list`. Sharing would mean parameterizing the tail, which is a knob for one caller each.
// Recorded rather than left implicit: this is the second place the param/result ordering is written,
// and TestOrderedTypeUseChainsAgree is what stops the two from drifting.
//
// The 30 field-ordering vectors split across both readers — block.wast/loop.wast/if.wast reach
// `blockSignature`, call_indirect.wast/return_call_indirect.wast reach this one — so a drift between
// them is a drift the board would show only in one of two files.
// **Its sugar arm interns unconditionally** (:847: `inline_functype c ft $loc($1)`), with no
// `([], [t])` case — so `(call_indirect (result i32) …)` creates a type where `(block (result i32))`
// does not. Hence callchain rather than blockchain, and see signatureKind for why that is a
// parameter and not read off the tail.
func (p *parser) callexprType() error {
	return p.orderedTypeUse(callchain, p.exprList)
}

// selectResults parses the `(result …)*` chain both `select` forms carry, then the given tail:
// `selectexpr_results` (parser.mly:836-840) for the folded arm, `selectinstr_results_instr_list`
// (:682-686) for the flat one. The two differ only in that tail — `expr_list` against `instr_list`.
//
// No `(param)` half, which is the arm's peculiarity, so this is not orderedTypeUse with an empty
// loop: `select` takes no type use and no params at all. The reference tracks *whether* a result was
// written (the `b` in `true, snd $3 c @ ts, es`) because `select` unannotated is a different
// instruction from `select (result t)` at validation time. This stratum reads both and distinguishes
// neither — arity and typing are validation's.
// **Nothing is interned here**, which is the other half of the arm's peculiarity and worth stating
// beside the missing `(param)`: the results go into `select (Some ts)` directly (:678), never through
// `inline_functype`. So `select` is the one member of the extended family that leaves the type space
// alone entirely — not blockchain with an empty param list, and not a conditional intern either.
func (p *parser) selectResults(tail func() error) error {
	p.c.next() // SELECT
	if _, err := p.functypeResult(); err != nil {
		return err
	}
	return tail()
}

// handlerClauses parses `try_block_handler_body` (parser.mly:917-929): the `(catch …)` clauses that
// precede a folded `try_table`'s body.
//
// The same four arms `handlerBlock` reads for the flat form, and split out of it so both callers
// share one clause reader — the flat path reads a signature then clauses then an `instr_list`
// terminated by `end`, the folded path the same minus the terminator. All four arms are read though
// no vector reaches the last two: a handler set missing an arm rejects a legal module, which is the
// accept-direction class decision 0007 says the suite cannot falsify.
//
// **A clause's label resolves in the ENCLOSING scope, not the try_table's own** (#80), and this is
// the one label fact the grammar states twice and no vector can check. All four arms read
// `($4 c label)` — `c`, where the body reads `c'` (:797-806, :934-943). The suite's own use is
// `try_table.wast:30`:
//
//	(block $h (try_table (result i32) (catch $e0 $h) …))
//
// `$h` is the enclosing block's, and a handler that branched to the try_table itself would be a
// loop. Resolving in `c'` would still *find* `$h` — it is on the stack either way — and would
// merely compute an index one too large, which is invisible at this stratum and wrong at the next.
// What it would also do is accept `(try_table $t (catch $e $t) …)`, a clause naming the try_table's
// own label, which the reference rejects as unknown. That spelling is nowhere in the suite, so the
// control is the oracle here.
func (p *parser) handlerClauses() error {
	// The try_table's own label is already pushed by the caller (blockinstr/foldedBlock), so the
	// enclosing scope is one level down. Popped for the clauses and restored before the body, which
	// is the mutable-context spelling of the reference passing `c` to the clauses and `c'` to
	// `instr_list`.
	own := p.ctx.labels.labelReset()
	p.ctx.labels.labelRestore(own[:len(own)-1])
	restored := false
	restoreOwn := func() {
		if !restored {
			p.ctx.labels.labelRestore(own)
			restored = true
		}
	}
	defer restoreOwn()
	for p.c.at(LParen) {
		t := p.c.peek2()
		if t.Kind != KeywordTok {
			break
		}
		var idxs int
		switch t.Keyword {
		case kwCatch, kwCatchRef:
			idxs = 2 // `(catch $tag $label)` — a tag and a label
		case kwCatchAll, kwCatchAllRef:
			idxs = 1 // `(catch_all $label)`
		default:
			// Not a handler clause: a folded instruction opening the body, which reads `c'` — so the
			// try_table's own label goes back before the body is read.
			restoreOwn()
			return p.instrList()
		}
		p.c.next() // the LPAR
		p.c.next() // the keyword
		// The **last** index of every arm is the label; `catch`/`catch_ref`'s first is a tag, which
		// this stratum does not resolve (tags need the deferred phase — a `(tag $e)` may be defined
		// after the func that catches it). Written as "all but the last are unresolved" rather than
		// per-arm because that is what the four arms have in common: `($3 c tag) ($4 c label)` and
		// `($3 c label)`, the label always final.
		for i := range idxs {
			if i == idxs-1 {
				if err := p.labelIdx(); err != nil {
					return err
				}
				continue
			}
			if err := p.idx(); err != nil {
				return err
			}
		}
		if err := p.rpar(); err != nil {
			return err
		}
	}
	restoreOwn() // the body reads `c'`
	return p.instrList()
}

// expr parses `LPAR expr1 RPAR` (parser.mly:809), the folded form — restricted to `expr1`'s
// first arm, `plaininstr expr_list` (:813).
//
// **This is #63's, not #64's, and the seam is defect ownership rather than surface form.** The
// arm is pure sugar and transports its token stream to a defect that lives in one of #63's
// immediate readers: `(i32.const 0x100000000)` is a `constant out of range` from constImm no
// matter which spelling delivered it, and 353 of the 390 reachable vectors write their
// instructions folded because that is how a `(func …)` body reads. Leaving the arm to #64 would
// have parked those 353 behind a sugar rewrite they do not need. (Ruling: Scott — children exist
// to earn buckets, and a bucket belongs to the production that must be fixed.)
//
// **All ten arms are now read (#64).** The other nine were #64's, and the paragraph above described
// the seam while it was live; it is kept because it records *why* the split fell where it did.
func (p *parser) expr() (bool, error) {
	if !p.c.at(LParen) {
		return false, nil
	}
	// The mnemonic decides whether this is a fold at all, and it is two tokens away — so the check
	// is peek2 rather than a consume-and-backtrack. A `(` whose keyword starts no instruction is
	// not an `expr`, and this must not have eaten the paren by the time the caller reports it:
	// `(func (param i32))` reaches here through funcField and the `(param` must survive.
	t := p.c.peek2()
	if t.Kind != KeywordTok || !startsInstruction(t.Keyword) {
		return false, nil
	}
	p.c.next() // the LPAR
	if err := p.expr1(t.Keyword); err != nil {
		return true, err
	}
	return true, p.rpar()
}

// expr1 parses one folded instruction's interior — everything between the `(` and the matching `)`
// (parser.mly:813-834). The leader has been peeked but not consumed.
//
// **Ten arms, and they divide by what follows the leader rather than by what the leader is.** Six
// of the nine non-plain arms are two productions wearing an optional index:
// `CALL_INDIRECT idx callexpr_type` and `CALL_INDIRECT callexpr_type` (:817/:819) differ only in
// whether the table index is written, and `RETURN_CALL_INDIRECT` (:821/:823) repeats that exactly.
// Menhir needs them as separate arms because it decides by lookahead; a recursive-descent reader
// asks "is the next token an idx?" and has one arm.
//
// The block arms delegate to readers the flat family already built (#63): `block` for BLOCK/LOOP,
// `handlerBlock`'s signature half for TRY_TABLE. That reuse is the point rather than a convenience
// — `blockSignature` is where the param/result ordering and the *absence* of a `bindidx` in
// `block_param_body` (:756) are decided, and a second folded implementation would be a second place
// for `(block (param $x i32))` to be wrongly admitted. It was wrongly admitted once already, by
// delegating to `functype` (grave, #63), and TestFoldedAndFlatSignaturesAgree is what holds the
// two paths to one answer.
//
// What the folded forms do *not* have is an `END`, so extent comes from the closing paren and there
// is no `labeling_end_opt`: `(block $a … )` cannot mismatch its own end label because it has none.
func (p *parser) expr1(leader keywordKind) error {
	switch leader {
	case kwBlock, kwLoop, kwIf, kwTryTable:
		return p.foldedBlock(leader)
	case kwSelect:
		return p.selectResults(p.exprList)
	case kwCallIndirect, kwReturnCallIndirect:
		p.c.next() // the keyword
		// `idx` is optional here as a *sugar arm* (:819/:823), not as an `idx_opt`: the table
		// index may be a NAT or a VAR, and anything else starts `callexpr_type`.
		if p.c.at(NatTok) || p.c.at(VarTok) {
			if err := p.idx(); err != nil {
				return err
			}
		}
		return p.callexprType()
	default:
		// Falls out of the switch to the first arm below. Written as an empty default rather than
		// left implicit because `exhaustive` reads a bare fall-through as an unhandled enum, and
		// the honest statement is that this switch dispatches nine arms out of 173 keyword kinds
		// and hands everything else to `plaininstr` — which is a fallback, not an omission.
	}
	// `plaininstr expr_list` (:814), the arm #63 owned.
	if _, err := p.plaininstr(); err != nil {
		return err
	}
	return p.exprList()
}

// exprList parses `expr_list` (:946-948): zero or more folded operands, each itself an `expr`.
//
// Its empty arm is why `(i32.const 0)` needs no operands and `(i32.add (i32.const 1) (i32.const 2))`
// takes two — the *arity* is validation's business, not this stratum's, so nothing here counts them.
//
// A `(` that is not an `expr` ends the list rather than erroring, because several callers have
// something legal after the operands: `callexpr_results` (:858) reaches `expr_list` with `(param`
// and `(result` already excluded by the loops above it, and a stray one there is the enclosing
// production's error to report, at its own position.
func (p *parser) exprList() error {
	for {
		read, err := p.expr()
		if err != nil {
			return err
		}
		if !read {
			break
		}
	}
	if !p.c.at(RParen) {
		return p.unexpected()
	}
	return nil
}

// expectedInstr reports that an instruction was required here and none starts.
//
// **It was `bodyBoundary`, and the history is the documentation** — the boundary was where the
// module stratum stopped, and its whole design problem was telling *this parser is unfinished*
// apart from *this module is malformed*. Three rulings narrowed the first claim until it was empty,
// and the sections below are those rulings in the order they landed; the arm they governed is gone,
// but the reasoning is what stops it being reinvented.
//
// It was a named error rather than a silent accept or a generic complaint, because the board buckets
// by expected string and that bucket *was* the work plan — a module rejected for an unwritten reader
// had to be legible as that rather than masquerading as a syntax error.
//
// **A closing paren here is a syntax error, not a boundary.** Every caller that reaches this
// rather than instrList has a grammar requiring at least one instruction — `constexpr1` (:951),
// `offset` (:1091, mandatory where it appears), the sugar arm's leading `elemexpr` (:1205) — so
// `(data (memory 0))` with no offset is malformed and this stratum can say so on the merits.
// Claiming `unimplemented` for it would be the wrong-layer error in the direction that flatters
// us: a module the reference rejects, parked in the boundary bucket as though finishing the
// instruction grammar would make it legal. The callers whose empty case *is* legal check for the
// paren before calling, which is what makes this reachable only when it is true.
//
// Built through errf rather than as an `&Error{…}` literal, which is a correction: the first draft
// composed the struct here and so became a *fourth* error constructor in a package that documents
// three. TestErrorConstructorsAreAccountedFor found it by sweeping the AST for `&Error{}` literals
// — a control derived from the source rather than from the list it checks, which is the only reason
// an unlisted site was findable at all.
// **A handler clause in instruction position is malformed in every production, so it is not a
// boundary either.** `(catch …)`, `(catch_ref …)`, `(catch_all …)` and `(catch_all_ref …)` appear in
// exactly two places in the whole grammar — `handler_block_body` (:792-:806) and
// `try_block_handler_body` (:929) — and both consume their clauses *before* the `instr_list`. They
// are not arms of `expr1` (:813-:834), so no folded form can start with one, and #64 will never
// grow a reader for `(module (func (catch_all)))`. Claiming `unimplemented` for it promises work
// that does not exist: `try_table.wast:366` and `:371` are `unexpected token` vectors on precisely
// those two spellings, and they would sit in #64's inventory forever, unanswerable by finishing it.
//
// **The two candidate fixes are distinguished by one input, and finding it took a falsification run
// that did not fail.** TestBlockTerminatorsEndTheList caught `try_table nop (catch 0 0) end`
// reporting the boundary; the reflex repair was to add the clauses to atBlockTerminator instead. The
// falsification pass for *that* variant — clauses in the terminator set, this check removed — was
// expected to fail the control and **passed**, which is the same class as the three-deep precedence
// claim earlier in this PR: a structural argument, stated as a rule, that no assertion held.
//
// Probing rather than arguing produced the discriminator: `(module (func (drop (catch_all))))`. The
// variant reports it *unimplemented*, because a folded operand reaches this function through
// `expr`'s operand loop (:1254) rather than through `instrList`, and a terminator set cannot see
// that path. On every other row the two variants agree — same verdict, and positions differing only
// by the paren each blames. So the check belongs here, at the one place all three routes to a
// stray clause converge, and the reason is a measured input rather than a layering preference. The
// masked-syntax-error worry about widening the terminator set is real but was not what settled it.
//
// **The set is now derived rather than listed, which is #70, and the "zero vectors" claim that
// filed it was wrong by twelve.** The handler clauses were only a *sample* of the
// instruction-position tokens no stratum will ever read; the general predicate is
// startsInstruction, `shapeOf`'s domain plus `expr1`'s seven non-plaininstr leaders. A `(` whose
// keyword cannot start any instruction is `unexpected token` on the merits, and the clauses fall
// out of that without being named.
//
// I filed #70 asserting no vector turned on the generalization, having probed five spellings I
// thought of — `(memory 1)`, `(then …)`, `(else …)`, `(global i32)`, a flat
// `block (result i32) (param i32)` — and found none in the corpus. Measured by patching this
// function and reading the board instead: **1941 → 1953 pass, 79 → 67 fail.** The twelve are
// `func.wast`'s field-ordering vectors — six type-use permutations at :559 :566 :573 :580 :587
// :594 and six field-after-body forms at :937 :941 :945 :949 :953 :957, all `unexpected token`,
// itemized by line at spec_test.go's textFailCeiling. They are the class the five probes
// could not reach, because I was looking for *unreachable keywords* and these are **reachable
// keywords in an unreachable position**: `(func (result i32) (param i32) …)`,
// `(func (local i32) (param i32))`, `(func (nop) (local i32))`. `func_body` is `instr_list`
// (:1017), which cannot begin with `(param`/`(local`/`(result`/`(type`, so each is a plain syntax
// error — and `(func (local i32) (param i32))` contains no instructions at all, so promising an
// instruction-body reader for it was the wrong-layer error with a board cost.
//
// The lesson is the second-order one, and it was already in CLAUDE.md when I broke it: a
// "zero vectors" figure is exactly as falsifiable as any other board claim, and *the board is the
// instrument*. I measured with my imagination and quoted the result as a finding.
//
// Position-dependence comes free and is worth naming, because it is what makes the check narrow
// enough to be safe: this function only runs where an instruction was required, so
// `(func (param i32))` still parses — a lone `(param …)` is legal in `func_fields`, and only after
// a result/local/body does it become misplaced. Nothing here knows about field ordering; the
// caller's position does.
//
// # The boundary has no subject left, so it is a syntax error in every case (#64)
//
// **The `unimplemented` arm is gone, and what retires it is a measurement of the space rather than
// a feeling that the grammar looks finished.** `unimplemented` is a promise that a later stratum
// will read the token, and the folded work is the last stratum: all four `instr_list` arms
// (parser.mly:546-550) and all three `instr1` arms (:552-554) now have readers. So the claim to
// check is that no token this function can see is one a future reader would claim — and the
// checkable form of that is *every leader `startsInstruction` admits is consumed by some reader*.
//
// Measured by asking, for each of the 494 keywords the generated table maps to an
// instruction-starting kind, whether `ReadModule("(module (func <kw>))")` blames an offset **at**
// the keyword — no reader claimed it — or past it. **493 of 494 are consumed.** The single
// exception is `i8x16.shuffle`, which blames its own offset for `wrong number of lane indices`:
// that *is* a reader claiming the mnemonic and then rejecting its immediates, which is the
// opposite of unread. So nothing admitted is unread, and by construction nothing unadmitted can
// ever become readable — `startsInstruction` is the union of the readers' own domains.
//
// The inverse sweep is the half that found a defect. 93 keywords reached the `unimplemented` arm,
// and `startsInstruction` says no to every one of them: `(func param)`, `(func local)`,
// `(func mut)`, `(func i32)`, plus a bare NAT, string or VAR (`(func 5)`, `(func "abc")`). Those
// are **#70's defect on the unparenthesized side** — the derived check only looked past a `(`, so a
// bare structural keyword in instruction position still got the boundary's promise. `func_body` is
// `instr_list` and a bare `param` cannot begin one, so each is a plain syntax error and no reader
// will ever be written for it. Same wrong-layer error in the same flattering direction, one paren
// shallower, and it survived #70 because #70's own probes all had parens in them.
//
// Board effect: **none**, and this time that is a reading rather than a forecast — 1992/28 before
// and after, per file. The class is real and the corpus has no vector for it, which is exactly the
// case the overfitting rule says to fix anyway: a spec string nobody expects is still a lie about
// the module. The board's `unimplemented` column was already 0 before this change, because the
// vectors that reached the arm were the ones the folded readers had just answered.
func (p *parser) expectedInstr() error {
	if p.c.at(LParen) {
		return p.unexpectedAt(p.c.peek2())
	}
	return p.unexpected()
}

// lpar consumes `( keyword`, the opening of a parenthesized form.
//
// Both tokens together because every caller wants both and checking them separately invites the
// half-consumed cursor: a production that ate the LPAR and then failed on the keyword leaves the
// stream somewhere no caller expects. Not a correctness issue while every error is terminal, and
// a trap the moment anything backtracks.
func (p *parser) lpar(k keywordKind) error {
	if !p.c.at(LParen) {
		return p.unexpected()
	}
	if !p.c.peek2Keyword(k) {
		return p.unexpectedAt(p.c.peek2())
	}
	p.c.next()
	p.c.next()
	return nil
}

// rpar consumes a closing parenthesis.
func (p *parser) rpar() error {
	if !p.c.at(RParen) {
		return p.unexpected()
	}
	p.c.next()
	return nil
}

// unexpected reports the reference's `unexpected token` at the cursor.
//
// The message is the sentinel the suite's 152-vector bucket expects. It does **not** name the
// token: the reference's is menhir's, generated from the parse state, and reconstructing that
// text would be inventing evidence about the parser's internals (grave #36). The sentinel is
// what the oracle reads; anything past it would be ours to defend and indefensible.
func (p *parser) unexpected() error { return p.unexpectedAt(p.c.peek()) }

// unexpectedAt is unexpected at an explicit token, for the lookahead cases where the offending
// token is not the one the cursor sits on.
func (p *parser) unexpectedAt(t Token) error {
	return &Error{Msg: "unexpected token", Offset: t.Offset, Line: t.Line}
}
