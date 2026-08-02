package text

// The wat parser: recursive descent over the reference's grammar, returning an error and
// nothing else (decision 0011).
//
// **Scope, stated as a boundary rather than as a to-do.** This stratum parses module *fields*
// and the type algebra. It does not descend into instruction bodies — `plaininstr`,
// `blockinstr`, `expr`, `constexpr`, and the folded forms are #63/#64's, and a `(func …)` whose
// body holds instructions stops here with `unexpectedTokenAtInstruction`.
//
// Which of those two owns which is settled by **defect ownership, not surface form** (Scott's
// ruling on #63's forecast): `expr`/`expr1`'s minimal arm (parser.mly:809/:814) is #63's, since
// it only transports the token stream to a defect in an immediate reader, and #64 owns the
// desugaring *families* — `callexpr_*`/`selectexpr_*` (:836–:865), `if_`/`try_block`
// (:891/:901). `constexpr` (:950) is listed above as a boundary this stratum does not cross,
// which it is, but it is **not sugar**: the reference marks `expr`/`expr1` `/* Sugar */` and
// `constexpr` not at all — it is a plain alias for `instr_list`.
//
// The boundary is a
// named error rather than a silent accept because *an error from the wrong layer is evidence
// about where structure was lost*: a module rejected for having a body should say so, not
// produce a spurious complaint about a parenthesis.
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
func (p *parser) moduleFields() error {
	for p.c.at(LParen) {
		if err := p.moduleField(); err != nil {
			return err
		}
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
func (p *parser) typeDef() error {
	if err := p.lpar(kwType); err != nil {
		return err
	}
	if err := p.bindidxOpt(&p.ctx.types, "type"); err != nil {
		return err
	}
	if err := p.subtype(); err != nil {
		return err
	}
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
	case kwFunc:
		if err := p.lpar(kwFunc); err != nil {
			return err
		}
		if err := p.bindidxOpt(&p.ctx.funcs, "func"); err != nil {
			return err
		}
		if p.atTypeuse() {
			if err := p.typeuse(); err != nil {
				return err
			}
		} else if err := p.functype(); err != nil { // sugar, parser.mly:1246
			return err
		}
		return p.rpar()
	case kwTag:
		if err := p.lpar(kwTag); err != nil {
			return err
		}
		if err := p.bindidxOpt(&p.ctx.tags, "tag"); err != nil {
			return err
		}
		if p.atTypeuse() {
			if err := p.typeuse(); err != nil {
				return err
			}
		} else if err := p.functype(); err != nil { // sugar, parser.mly:1234
			return err
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

// funcField parses `func` (parser.mly:959-962) down to its body, where this stratum stops.
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
	imported, err := p.inlineImport()
	if err != nil {
		return err
	}
	if p.atTypeuse() {
		if err := p.typeuse(); err != nil {
			return err
		}
	}
	if imported {
		// func_fields_import (parser.mly:991-1001): params and results, no body, no locals.
		if err := p.functype(); err != nil {
			return err
		}
		return p.rpar()
	}
	if err := p.markFuncSignature(); err != nil {
		return err
	}
	if err := p.locals(); err != nil {
		return err
	}
	p.ctx.markDefined(importFunc)
	// func_body is instr_list (parser.mly:1019), whose empty arm makes `(func)` well-formed.
	if err := p.instrList(); err != nil {
		return err
	}
	return p.rpar()
}

// markFuncSignature parses func_fields_body's params and results (parser.mly:1003-1016) and
// binds the params into the local index space.
//
// Params bind as locals — `anon_locals c (fst $3)` at :1006 and `bind_local c $3` at :1009 — so
// `(func (param $x i32) (local $x i32))` is `duplicate local $x`. That is one of #62's 13
// duplicate-binding vectors and it only works if params and locals share one space.
//
// The locals space is reset per function, since it is `enter_func`'s (parser.mly:965).
func (p *parser) markFuncSignature() error {
	p.ctx.locals = space{}
	for p.c.at(LParen) && p.c.peek2Keyword(kwParam) {
		if err := p.lpar(kwParam); err != nil {
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
			if err := p.valtype(); err != nil {
				return err
			}
		} else {
			n, err := p.valtypeList()
			if err != nil {
				return err
			}
			for range n {
				p.ctx.locals.bindAnon()
			}
		}
		if err := p.rpar(); err != nil {
			return err
		}
	}
	return p.functypeResult()
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
			if err := p.valtype(); err != nil {
				return err
			}
		} else {
			n, err := p.valtypeList()
			if err != nil {
				return err
			}
			for range n {
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
	if p.atTypeuse() {
		if err := p.typeuse(); err != nil {
			return err
		}
	}
	if err := p.functype(); err != nil {
		return err
	}
	if !imported {
		p.ctx.markDefined(importTag)
	}
	return p.rpar()
}

// globalField parses `global` (parser.mly:1074-1089).
//
// The non-import arm is `globaltype constexpr`, and constexpr is an instruction sequence — so
// this is the second place the stratum's boundary shows. `(global i32 (i32.const 0))` reaches
// bodyBoundary; `(global (import "m" "g") i32)` completes.
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
		if err := p.reftype(); err != nil {
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
	if err := p.reftype(); err != nil {
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
	if p.c.at(LParen) && !p.c.peek2Keyword(kwItem) {
		// The offset sugar (parser.mly:1171/:1175): `offset elem_list` or `offset elemidx_list`.
		// An `(item …)` here is an elemexpr in the *passive* arm's elem_list instead, which is
		// what the lookahead separates — the two arms start with the same paren and only the
		// keyword after it decides, so this is peek2 rather than a trial parse.
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
		if err := p.reftype(); err != nil {
			return err
		}
		return p.elemexprList()
	}
	if p.c.at(RParen) {
		return nil
	}
	return p.bodyBoundary()
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
// of the second, and the minimal case of the third. What is left unread stops at bodyBoundary as
// before, so the bucket shrinks rather than moving.
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
		read, err := p.instr1()
		if err != nil {
			return err
		}
		if !read {
			return p.bodyBoundary()
		}
	}
}

// constexpr1 parses `instr1 instr_list` (parser.mly:953): at least one instruction.
//
// The distinction from instrList is the whole reason bodyBoundary rejects a closing paren: for
// these callers an empty sequence is a *syntax error*, so `(data (memory 0))` with no offset is
// malformed on the merits rather than unimplemented. Both halves of that ruling now have code:
// the empty case is `unexpected token` from bodyBoundary, and a non-empty one this stratum can
// read completes.
func (p *parser) constexpr1() error {
	read, err := p.instr1()
	if err != nil {
		return err
	}
	if !read {
		return p.bodyBoundary()
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
		return p.bodyBoundary()
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
		return p.bodyBoundary()
	}
	return nil
}

// instr1 parses one instruction (parser.mly:552-554), reporting whether it read one.
//
// Three arms, and #63 owns two and a half of them: `plaininstr`, `blockinstr` in its **flat**
// form, and the `plaininstr expr_list` arm of `expr1` reached through `expr`. `expr1`'s other
// nine arms are #64's, so this returns false for them and the caller falls through to
// bodyBoundary — which keeps the board's unread work in one legible bucket instead of scattering
// it across arms.
//
// The false return must leave the cursor untouched, since bodyBoundary reports the token it stops
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
// the clauses out is right, and it left a stray `(catch …)` falling through to bodyBoundary to be
// reported as *unimplemented* — a rejection with the wrong layer's name on it. The clause set is
// now named at bodyBoundary, where the false claim was, rather than here, where it would have
// bought a masked syntax error. See that function's header for the measurement.
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
	if err := p.blockSignature(); err != nil {
		return err
	}
	return p.instrList()
}

// blockSignature reads the part `block` and `handler_block` have in common: `typeuse?` then the
// `(param …)*` and `(result …)*` lists, stopping before the body.
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
func (p *parser) blockSignature() error {
	if p.atTypeuse() {
		if err := p.typeuse(); err != nil {
			return err
		}
	}
	// `block_param_body` then `block_result_body`, in that order and each its own loop: a
	// `(param)` may not follow a `(result)`, and one loop accepting either would admit a form the
	// reference rejects. The `valtype_list` is unnamed — see the header.
	for p.c.at(LParen) && p.c.peek2Keyword(kwParam) {
		if err := p.lpar(kwParam); err != nil {
			return err
		}
		if _, err := p.valtypeList(); err != nil {
			return err
		}
		if err := p.rpar(); err != nil {
			return err
		}
	}
	return p.functypeResult()
}

// handlerBlock parses `handler_block` (:853-:864) — `try_table`'s block, whose body is preceded by
// zero or more `(catch …)` clauses.
//
// Four handler arms (:874-:806), and all four are read even though no vector reaches the last
// two: a handler set missing an arm rejects a legal module, and that is the accept-direction class
// decision 0007 says the suite cannot falsify.
func (p *parser) handlerBlock() error {
	if err := p.blockSignature(); err != nil {
		return err
	}
	for p.c.at(LParen) {
		t := p.c.peek2()
		if t.Kind != KeywordTok {
			break
		}
		var idxs int
		switch t.Keyword {
		case kwCatch, kwCatchRef:
			idxs = 2 // `(catch $tag $label)` — a tag and a label
		case kwCatchAll:
			idxs = 1 // `(catch_all $label)`
		case kwCatchAllRef:
			idxs = 1
		default:
			// Not a handler clause: a folded instruction opening the body, which instrList
			// handles (or reports as the boundary).
			return p.instrList()
		}
		p.c.next() // the LPAR
		p.c.next() // the keyword
		for range idxs {
			if err := p.idx(); err != nil {
				return err
			}
		}
		if err := p.rpar(); err != nil {
			return err
		}
	}
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
// `expr1`'s other arms — SELECT, CALL_INDIRECT, BLOCK, LOOP, IF, TRY_TABLE and their sugar — are
// #64's and return false here, unconsumed.
func (p *parser) expr() (bool, error) {
	if !p.c.at(LParen) {
		return false, nil
	}
	// The mnemonic decides whether this fold is ours, and it is two tokens away — so the check is
	// peek2 rather than a consume-and-backtrack. A `(` whose keyword is a blockinstr or one of
	// expr1's own arms belongs to #64, and this must not have eaten the paren by the time
	// bodyBoundary reports it.
	t := p.c.peek2()
	if t.Kind != KeywordTok {
		return false, nil
	}
	if _, ok := shapeOf(t.Keyword); !ok {
		return false, nil
	}
	p.c.next() // the LPAR
	if _, err := p.plaininstr(); err != nil {
		return true, err
	}
	// `expr_list` (:946-948): zero or more folded operands, each itself an `expr`. Its empty arm
	// is why `(i32.const 0)` needs no operands and `(i32.add (i32.const 1) (i32.const 2))` needs
	// two — the *arity* is validation's business, not this stratum's, so nothing here counts them.
	for {
		read, err := p.expr()
		if err != nil {
			return true, err
		}
		if !read {
			break
		}
	}
	// A folded operand this stratum cannot read is still an unread body, and it must be reported
	// as one rather than as a syntax error: the cursor is on a `(` that #64 will handle, and
	// `unexpected token` here would be the wrong-layer error in the direction that flatters us.
	if !p.c.at(RParen) {
		return true, p.bodyBoundary()
	}
	return true, p.rpar()
}

// bodyBoundary is where this stratum stops: a non-empty instruction sequence, which #63/#64 own.
//
// A named error rather than a silent accept or a generic complaint, because the board buckets by
// expected string and this bucket *is* the work plan — a module rejected here is a module whose
// only defect is that the parser is unfinished, and it must be legible as that rather than
// masquerading as a syntax error. Under the harness this scores as a fail against whatever the
// vector expected, which is honest: the vector's question has not been answered.
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
func (p *parser) bodyBoundary() error {
	if p.c.at(RParen) {
		return p.unexpected()
	}
	if p.c.at(LParen) {
		kw := p.c.peek2()
		if kw.Kind != KeywordTok || !startsInstruction(kw.Keyword) {
			return p.unexpectedAt(kw)
		}
	}
	t := p.c.peek()
	return errf(t, "unimplemented: instruction body at %q", t.Text)
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
