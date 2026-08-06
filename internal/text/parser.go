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

	// retain is the *mode*: whether this parse is building a module or only judging one.
	//
	// Set by `parseModule` from its caller, and read in exactly one place — `funcField`, which
	// installs the sink. Everything downstream asks `retaining()` instead, so the mode is consulted
	// once and the sink's nil-ness carries it the rest of the way.
	//
	// It exists because retention is not free of consequences: the instruction frontier
	// (`refuseUnencodable`) *errors* on well-formed input it cannot encode, and `ReadModule` must
	// never do that. The first version of this file installed the sink unconditionally, and the suite
	// said what that costs — 40-odd rows across four tests demanding `mismatching label` or `unknown
	// type 1` and getting `cannot yet encode the block instruction`. That is the accept-direction
	// failure this package's whole discipline is about, caught here only because those tests assert
	// the *specific* error rather than mere rejection.
	retain bool
	// sink is where retained instructions go, and nil means "recognize only" — see code.go's
	// header for why this is a field rather than fourteen parameters, and why nil is silent.
	sink *instrSink
	// imm accumulates the current instruction's immediate bytes. Reset per instruction by
	// plaininstr, which is the one function that knows where an instruction begins.
	imm []byte
	// immPatch defers the current instruction's immediate to stage 2, for the one category that
	// can forward-reference: a `call $f` naming a function defined later. Set by the index
	// reader, consumed and cleared by plaininstr.
	immPatch func() ([]byte, error)
	// localsMissParams is non-zero while the func being parsed has a typeuse whose params this
	// stratum could not bind into `p.ctx.locals` — see funcField, and #77.
	//
	// **A flag rather than a refusal at the func**, because the refusal has to be *narrow*: a typeuse
	// whose referenced type happens to have no params costs nothing, and refusing every typeuse threw
	// away `(module (func (type $t)) (type $t (func)))`, an encodable row already in the round-trip
	// table. The count is unknowable at this cursor (the type may be defined later), so the honest
	// predicate is not "how many params" but "is a local index about to be resolved against a space
	// that may be short" — which is a question only `retainIdx` is standing at.
	//
	// It carries the typeuse's own token so the message points at `(type $sig)`, the thing that is
	// unencodable, rather than at the `local.get` that merely revealed it.
	//
	// **A separate bool, not a zero-Token test**, and that is grave #120's lesson one type over:
	// `LParen` is `TokenKind`'s ordinal 0, so `tok.Kind != 0` reads a zero value as a paren token and
	// the guard would fire in every func. The zero value of a struct is not a sentinel unless someone
	// made it one — `spaceKind` has `spaceUnset` for exactly this and `Token` has no equivalent.
	localsMissParams    bool
	localsMissParamsTok Token
	// funcs is the retained body of every *defined* function, in definition order — which is
	// the order sections 3 and 10 both require, and the reason they are fed from one list
	// (encode.ml:1141/:1159). Imported funcs are absent: they have no body and their type index
	// is the import descriptor's.
	funcs []textFunc
}

// ReadModule reports whether src is a well-formed wat module, to the depth this stratum
// reaches.
//
// The signature is `spec.ReadTextFunc` exactly (decision 0011): error-only, no module value,
// because every vector in #62's bucket is a rejection and designing a module representation
// from reject vectors would fit it to code that never reads it. When well-formed modules are
// needed, 0011's second half applies — the parser emits binary bytes into the proven decoder,
// and `binary.Module` stays the codebase's one module authority.
// The body is `parseModule` with the parser discarded, which *is* what an error-only surface means
// (0011). Sharing the sequence rather than duplicating it: the three steps end in a trailing-token
// check that is easy to omit, and `EncodeModule` omitting it would accept `(module) (module)` as one
// module and encode the first — two places knowing one sequence, drifting silently (0006).
func ReadModule(src []byte) error {
	_, err := parseModule(src, recognize)
	return err
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
	// Everything but a type definition is past the encoder's frontier (#8). Recorded here, at the
	// dispatch, because this is the one place that sees every field kind exactly once — a check
	// added to each field's own function would be twelve places to forget one, and the omission
	// would be a *silently dropped section* rather than a compile error.
	// The exemption list grows as sections land, and it is a *list of keywords* rather than a check
	// each field makes, on the argument the paragraph above gives. `func` is exempt now because
	// section 3 and section 10 exist — but only conditionally: `funcField` withdraws the record at its
	// tail on the arms it can write and leaves it standing on the ones it cannot, which is why the
	// note is still taken here for every field and cleared there rather than skipped.
	if kw.Keyword != kwType && kw.Keyword != kwRec {
		// The *keyword* token, not the LPAR `p.c.peek()` would give: the message quotes the token's
		// text, and the LPAR's text is "(", which names nothing. Its offset is one byte later than
		// the field's start and is the more useful of the two anyway.
		p.ctx.noteNonTypeField(kw)
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
	if _, err := p.bindidxOpt(&p.ctx.types); err != nil {
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
//
// **The retention is `defineImport`'s, and the split matches the reference's arm exactly**:
// `fun c -> let df = $5 c in fun () -> Import ($3, $4, df ())` (:1251-1252) — the names and the
// slot at reduction, the descriptor in stage 2. That is not a coincidence to note in passing; it is
// why an import naming a forward-referenced type works, and it is the same three-stage structure
// typetable.go's header derives.
func (p *parser) importField() error {
	kw := p.c.peek2()
	tok := p.c.peek()
	if err := p.lpar(kwImport); err != nil {
		return err
	}
	p.ctx.noteImport(tok)
	module, err := p.name()
	if err != nil {
		return err
	}
	name, err := p.name()
	if err != nil {
		return err
	}
	fill, err := p.externtype()
	if err != nil {
		return err
	}
	// **Every arm of this field is encodable, so the withdrawal is unconditional** — the only field
	// kind of which that is true. There is no inline-export lookahead because `import` has no
	// inline-export arm (compare :1250 with :1084): the grammar, not a simplification.
	//
	// `inlineImportTail`'s shape rather than the helper itself, because the names come from this
	// production and not from an `inline_import`. The order is the same and for the same reason: paren
	// first, retention second, so a field that errors out mid-way leaves no trace.
	if err := p.rpar(); err != nil {
		return err
	}
	p.ctx.defineImport(module, name, fill)
	p.ctx.clearNonTypeField(kw)
	return nil
}

// externtype parses `externtype` (parser.mly:1227-1248) and returns a stage-2 thunk for its
// descriptor.
//
// Six kinds, and two of them have a sugar arm: `(func … typeuse)` versus `(func … functype)`,
// and the same for tag. Each binds into its own index space *before* the type is read, which is
// why an imported func shifts the func index even though it is not a definition — the fact that
// makes a count-based import-ordering check wrong.
//
// **The return is a thunk rather than a value, and the reference's own arm is written the same
// way** — `fun c -> ignore ($3 c anon_func bind_func); fun () -> ExternFuncT (Idx ($4 c).it)`
// (:1228-1229). The outer function binds the name at reduction; the inner one produces the
// descriptor in stage 2. Only the func and tag arms genuinely need the deferral, because only they
// hold a type index — but all five return thunks, because a signature that is a thunk for three
// arms and a value for two would push the branch into every caller, and there is a caller per
// spelling of an import.
//
// The closing paren is consumed **after** the switch rather than in each arm: every arm ends with one
// (all six productions are `LPAR … RPAR`), and six copies of a `return fill, p.rpar()` is six places
// to drop the paren check on the arm nobody re-reads. One arm forgetting it would accept
// `(import "m" "f" (memory 1)` — a missing paren — and the suite has no vector spelling that, because
// a malformed-paren wat is not what `assert_malformed` is usually written to catch.
func (p *parser) externtype() (func() (importDesc, error), error) {
	if !p.c.at(LParen) {
		return nil, p.unexpected()
	}
	kw := p.c.peek2()
	if kw.Kind != KeywordTok {
		return nil, p.unexpectedAt(kw)
	}
	// Only `fill` is hoisted. The first draft hoisted an `err` too, so the three payload arms could
	// write `if fill, err = …`, and every `if err := …` in the function then shadowed it — ten `govet`
	// shadow findings, and the fix is to make the arms uniform rather than to suppress them: each arm
	// takes its own `err` in its own scope, exactly as the func/tag arm already did.
	var fill func() (importDesc, error)
	switch kw.Keyword {
	case kwFunc, kwTag:
		// One arm for two kinds, because the reference's four arms (:1227-:1236 for the typeuse
		// forms, :1234/:1246 for the functype sugar) differ only in which index space the name
		// binds into. The *type* halves are identical, and this is the place a divergence between
		// them would be invisible: an imported tag and an imported func carry the same signature
		// grammar.
		//
		// They differ by **one byte** in the binary form — a tag writes an attribute `00` before the
		// index (encode.ml:191) — and that is the emitter's business, keyed on `desc.kind`, so the
		// two still share this arm. The kind is what the descriptor carries out of here.
		space, kind := &p.ctx.funcs, importFunc
		if kw.Keyword == kwTag {
			space, kind = &p.ctx.tags, importTag
		}
		if err := p.lpar(kw.Keyword); err != nil {
			return nil, err
		}
		if _, err := p.bindidxOpt(space); err != nil {
			return nil, err
		}
		// **No typeuse arm here takes an inline signature**, so neither helper is reached: the
		// reference's arms are `typeuse` alone or `functype` alone (compare with `func_fields`
		// :963, where both may appear). The sugar arm's `inline_functype` (:1236/:1248) is what
		// this calls, and its index used to be discarded — the interning's effect on
		// `len(typeCtx)` being the only part anything read. The index is now the descriptor.
		if p.atTypeuse() {
			use, err := p.typeuse()
			if err != nil {
				return nil, err
			}
			fill = p.externFuncDesc(kind, func() (uint32, error) { return p.ctx.resolveTypeIdx(use) })
		} else {
			ft, err := p.functype() // sugar, parser.mly:1234/:1246
			if err != nil {
				return nil, err
			}
			fill = p.externFuncDesc(kind, func() (uint32, error) { return p.ctx.internImplicit(ft) })
		}
	case kwGlobal:
		if err := p.lpar(kwGlobal); err != nil {
			return nil, err
		}
		if _, err := p.bindidxOpt(&p.ctx.globals); err != nil {
			return nil, err
		}
		// The type is dropped here and kept by the other caller: an `(import … (global …))` has no
		// initializer, so its globaltype is only ever read through the descriptor.
		_, f, err := p.importedGlobal()
		if err != nil {
			return nil, err
		}
		fill = f
	case kwMemory:
		if err := p.lpar(kwMemory); err != nil {
			return nil, err
		}
		if _, err := p.bindidxOpt(&p.ctx.memories); err != nil {
			return nil, err
		}
		f, err := p.importedMemory()
		if err != nil {
			return nil, err
		}
		fill = f
	case kwTable:
		if err := p.lpar(kwTable); err != nil {
			return nil, err
		}
		if _, err := p.bindidxOpt(&p.ctx.tables); err != nil {
			return nil, err
		}
		f, err := p.importedTable()
		if err != nil {
			return nil, err
		}
		fill = f
	default:
		return nil, p.unexpectedAt(kw)
	}
	if err := p.rpar(); err != nil {
		return nil, err
	}
	return fill, nil
}

// externFuncDesc wraps a stage-2 type-index producer as a func-or-tag descriptor.
//
// Its own function so the two arms above read as one shape, and because the *kind* is the only
// difference between them at this point — a reader comparing the func and tag paths should find
// nothing else to compare.
func (p *parser) externFuncDesc(kind importKind, idx func() (uint32, error)) func() (importDesc, error) {
	return func() (importDesc, error) {
		i, err := idx()
		if err != nil {
			return importDesc{}, err
		}
		return importDesc{kind: kind, typeIdx: i}, nil
	}
}

// importedGlobal, importedMemory and importedTable parse one non-func externtype's payload and
// return its stage-2 descriptor thunk.
//
// **Three functions rather than three inline arms because each has two call sites, one per import
// spelling.** `(import "m" "g" (global i32))` and `(global (import "m" "g") i32)` are the same
// import — the reference reaches `ExternGlobalT ($4 c)` from `externtype` :1240 and from
// `global_fields`'s inline-import arm :1085 — and a descriptor built at two sites is one fact stored
// twice, which is the shape #82's *one concept, one trigger* names. The failure it forbids here is
// specific and silent: a kind byte or a mutability flag written correctly in one spelling and wrongly
// in the other would still round-trip through the decoder for every vector that uses the spelling
// that works.
//
// It closes the other two of #111's four newly-rejecting modules, for the same reason
// `importedTable` closes its two — see that comment for the measurement and for why the count moved
// by four rather than by the two a per-site reading predicts.
// **It returns the parsed `globalType` as well as the thunk, and the pair is the point.** Both arms of
// `global_fields` read a `globaltype` and only one of them is an import, so the defining arm needs the
// *value* while the import arm needs the *descriptor* — and the helper stays the one reader either way.
// The alternative, a second `p.globaltype()` call in `globalField`'s defining arm, is one production
// with two readers, which is the shape #82's *one concept, one trigger* forbids and the shape grave
// #105 was filed for.
func (p *parser) importedGlobal() (globalType, func() (importDesc, error), error) {
	gt, err := p.globaltype()
	if err != nil {
		return globalType{}, nil, err
	}
	return gt, func() (importDesc, error) {
		rv, err := p.ctx.resolveVal(gt.val)
		if err != nil {
			return importDesc{}, err
		}
		return importDesc{kind: importGlobal, global: resolvedGlobal{val: rv, mut: gt.mut}}, nil
	}, nil
}

// importedMemory is the one arm with nothing to resolve — a `memorytype` is two numbers and a flag,
// and `defineMemory`'s comment has the grammar fact for why that needs no lookup. The thunk is still
// a thunk: see `externtype`'s signature comment for why uniformity beats a caller-side branch.
func (p *parser) importedMemory() (func() (importDesc, error), error) {
	mt, err := p.memorytype()
	if err != nil {
		return nil, err
	}
	return func() (importDesc, error) { return importDesc{kind: importMemory, mem: mt}, nil }, nil
}

// importedTable resolves an imported table's element type in stage 2.
//
// **This closes two of #111's ten accepting modules as a side effect of needing the value**, which is
// exactly how `defineTable` closed `(table 1 (ref null $u))`: the reftype's resolution was discarded
// along with its value, so an unknown type went unreported.
//
// **Two, not one, and the number was measured rather than counted** — the first draft of this comment
// said one, reasoning from the `(import …)` field spelling alone and forgetting that this helper is
// shared with the inline arm, which is the whole point of it being a helper. Re-running #111's own
// ten-row probe against `ReadModule` says four now reject: this helper's two table spellings and
// `importedGlobal`'s two global spellings. Six still accept. That is #111's own mechanical rule
// working as stated — *a site that threads the reftype into `resolveVal` rejects; a site that
// discards it accepts* — and it is why the issue counts **code sites** rather than modules.
//
// The fix is not scoped here: #111 stays open for the remaining six and holds the
// scope-to-the-space requirement for its control. Saying the count moved, and by how much, is the
// drifted-citation discipline rather than bookkeeping — a comment citing an issue's figures is a
// citation, and this sentence read "#111's table is now stale in four rows" until the export section
// PR re-ran the probe and posted the corrected table to the issue. Which is the half of that
// discipline easy to miss: noticing the staleness discharges nothing, and a comment reporting that a
// cited table is wrong is a citation pointing at a known error. The four rows are the two
// `importedTable` spellings and `importedGlobal`'s two.
func (p *parser) importedTable() (func() (importDesc, error), error) {
	tt, err := p.tabletype()
	if err != nil {
		return nil, err
	}
	return func() (importDesc, error) {
		rv, err := p.ctx.resolveVal(tt.elem)
		if err != nil {
			return importDesc{}, err
		}
		return importDesc{
			kind:  importTable,
			table: resolvedTable{addr64: tt.addr64, lim: tt.lim, elem: rv},
		}, nil
	}, nil
}

// exportField parses `export` (parser.mly:1265-1267): `(export name externidx)`.
//
// `duplicate export name` is **not** checked here. It is `valid.ml:1146`, the validator's — 26
// suite vectors, correctly outside this stratum, and measured before writing this so the figure
// could not drift into #62's forecast.
func (p *parser) exportField() error {
	kw, name, err := p.exportHead()
	if err != nil {
		return err
	}
	kind, ref, err := p.externidx()
	if err != nil {
		return err
	}
	space := p.ctx.spaceFor(kind)
	p.ctx.defineExport(name, kind, func() (uint32, error) { return space.resolveSpaceIdx(ref) })
	if err := p.rpar(); err != nil {
		return err
	}
	p.ctx.clearNonTypeField(kw)
	return nil
}

// exportHead consumes the `LPAR EXPORT name` both export spellings begin with, returning the
// keyword's token (for the frontier withdrawal) and the name.
//
// **It exists to be the one place the grammar counts an export.** `export` (:1265-1267) and
// `inline_export` (:1269-1274) diverge only after the name — one reads an `externidx`, the other
// takes its index from the enclosing field — so the shared prefix is a real production boundary and
// not a factoring convenience. That matters because the counter it increments is half of
// `encodableOrErr`'s withdrawal check, and `noteImport`'s comment owns the argument for why a
// counter belongs at a shared site: a call added at each spelling is two places to forget one, and
// the omission is a *silently short export section* rather than a compile error.
//
// The forgetting is not hypothetical here. The sugar spelling parsed and discarded its name for
// four PRs, and every board stayed green, because a module missing an export it never claimed to
// have is not a decode error.
func (p *parser) exportHead() (Token, string, error) {
	kw := p.c.peek2()
	if err := p.lpar(kwExport); err != nil {
		return Token{}, "", err
	}
	p.ctx.noteExport()
	name, err := p.name()
	if err != nil {
		return Token{}, "", err
	}
	return kw, name, nil
}

// externidx parses `externidx` (parser.mly:1258-1263): `(func idx)` and its four siblings.
//
// Returns the kind and the *unresolved* index. Unresolved because the resolution belongs to stage 2
// — `exports.wast:14` exports `(func $a)` before `$a` is bound — and the kind because the five arms
// differ in nothing else: each is `LPAR <keyword> idx RPAR`, and the keyword is the only fact the
// emitter needs beyond the number.
func (p *parser) externidx() (importKind, idxRef, error) {
	if !p.c.at(LParen) {
		return 0, idxRef{}, p.unexpected()
	}
	kw := p.c.peek2()
	if kw.Kind != KeywordTok {
		return 0, idxRef{}, p.unexpectedAt(kw)
	}
	var kind importKind
	switch kw.Keyword {
	case kwTag:
		kind = importTag
	case kwGlobal:
		kind = importGlobal
	case kwMemory:
		kind = importMemory
	case kwTable:
		kind = importTable
	case kwFunc:
		kind = importFunc
	default:
		return 0, idxRef{}, p.unexpectedAt(kw)
	}
	if err := p.lpar(kw.Keyword); err != nil {
		return 0, idxRef{}, err
	}
	ref, err := p.idxValue()
	if err != nil {
		return 0, idxRef{}, err
	}
	if err := p.rpar(); err != nil {
		return 0, idxRef{}, err
	}
	return kind, ref, nil
}

// inlineImport parses `inline_import` (parser.mly:1255-1256): `(import name name)` appearing
// *inside* a definition, which turns the definition into an import.
//
// Returns whether one was consumed, because the caller's grammar branches on it: a `(func
// (import …) …)` takes the func_fields_import arms and never has a body. It also means the field
// does **not** count as a definition for ordering purposes, which is the reference's `funcs <>
// []` being empty on those arms — a subtlety worth stating because "an inline import is still a
// func field" is the plausible wrong reading.
//
// **It returns the names too, and that is what makes the five sugar arms encodable (#8).** The
// reference's arm is `{ $3, $4 }` — a bare pair, no context function — precisely because the names
// are all it carries; the *descriptor* comes from the enclosing field's own grammar, which is why
// `(func (import "m" "f") (param i32))` and `(import "m" "f" (func (param i32)))` produce the same
// `Import`. A version of this that kept discarding the names would leave the section short by every
// inline-import occurrence in the corpus — 9 of the 143 modules the forecast measures, and silently,
// since a short vector is not a decode error.
type inlineImportNames struct {
	module string
	name   string
	have   bool
}

func (p *parser) inlineImport() (inlineImportNames, error) {
	if !p.c.at(LParen) || !p.c.peek2Keyword(kwImport) {
		return inlineImportNames{}, nil
	}
	tok := p.c.peek()
	if err := p.lpar(kwImport); err != nil {
		return inlineImportNames{}, err
	}
	p.ctx.noteImport(tok)
	module, err := p.name()
	if err != nil {
		return inlineImportNames{}, err
	}
	name, err := p.name()
	if err != nil {
		return inlineImportNames{}, err
	}
	return inlineImportNames{module: module, name: name, have: true}, p.rpar()
}

// inlineExports parses zero or more `inline_export` (parser.mly:1269-1274): `(export name)`
// inside a definition, retaining each as an export of `kind` at `idx`.
//
// A loop because the reference's arm is right-recursive over the whole field
// (`inline_export func_fields`), so `(func (export "a") (export "b"))` is legal. And they come
// *before* an inline import in the recursion, so `(func (export "a") (import "m" "f"))` parses
// while the reverse order does not — an ordering the arms encode and a paraphrase loses.
//
// **This spelling performs no lookup, and that is the reference's design rather than an omission.**
// `inline_export` is `fun d c -> Export ($3, d @@ $sloc)` (:1273): the index `d` arrives as a
// *parameter*, supplied by the enclosing field's arm as `$1 (FuncX x) c` (:987), where `x` is the
// field's own `bindidx_opt` result. So there is no name to resolve — the field being exported is the
// one the export is written inside, and `(func (export "a"))` cannot have a dangling reference the
// way `(export "a" (func $a))` can. `defineExport` still receives a thunk, because the *slot* order
// is what stage 2 preserves; the thunk simply closes over an index already known.
//
// The index is the field's own even for an inline *import*: `(memory (export "e") (import "m" "m") 1)`
// exports the imported memory, which occupies an index in the memory space like any other. That is
// why `bindidxOpt` runs before the arm split at every call site.
func (p *parser) inlineExports(kind importKind, idx uint32) error {
	for p.c.at(LParen) && p.c.peek2Keyword(kwExport) {
		_, name, err := p.exportHead()
		if err != nil {
			return err
		}
		if err := p.rpar(); err != nil {
			return err
		}
		// After the closing paren, per the rule every retention site here follows: a field that
		// errors out mid-way must leave no trace, or the retained counts disagree with the sections
		// on a module that never finished parsing.
		p.ctx.defineExport(name, kind, func() (uint32, error) { return idx, nil })
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
	kw := p.c.peek2()
	if err := p.lpar(kwFunc); err != nil {
		return err
	}
	idx, idxErr := p.bindidxOpt(&p.ctx.funcs)
	if idxErr != nil {
		return idxErr
	}
	if err := p.inlineExports(importFunc, idx); err != nil {
		return err
	}
	imp, impErr := p.inlineImport()
	if impErr != nil {
		return impErr
	}
	use, haveUse := idxRef{}, false
	if p.atTypeuse() {
		u, err := p.typeuse()
		if err != nil {
			return err
		}
		use, haveUse = u, true
	}
	if imp.have {
		// func_fields_import (parser.mly:991-1001): params and results, no body, no locals.
		ft, err := p.functype()
		if err != nil {
			return err
		}
		// **`defineImport` replaces the `deferSignature` call rather than joining it**, because the
		// signature work *is* the descriptor: the reference's arms are `ExternFuncT (Idx y.it)` where
		// `y` is whichever helper ran (:975-:983). Calling both would run the helper twice, and for the
		// sugar arm that is not merely wasteful — `inline_functype` appends, so a second call finds its
		// own first result and returns the same index, meaning the defect would be *invisible* here and
		// would surface only as a type-section count. Exactly the class `declareBlockImplicit`'s comment
		// warns about, reached from the other direction.
		return p.inlineImportTail(imp, kw, p.importedFuncDesc(importFunc, use, haveUse, ft))
	}
	ft, err := p.funcSignature()
	if err != nil {
		return err
	}
	typeIdx := p.deferSignature(use, haveUse, ft)
	// **A typeuse with no re-stated signature contributes its referenced type's params as anonymous
	// locals, and this stratum cannot compute them** — so `p.ctx.locals` is missing them and every
	// symbolic local index in the body is off by the param count. The flag says so; `retainIdx`
	// refuses at the index that would be wrong. See localsMissParams for why the refusal is there and
	// not here.
	//
	// `inline_functype_explicit`'s deferred branch binds them: `defer_locals c (fun () -> let (ts1,
	// _ts2) = func_type c x in bind "local" c.locals (length ts1) x.at)` (parser.mly:241-244), with the
	// reference's own comment at :239 saying why it is deferred. The count is `length ts1` of a type
	// that may be **defined later in the field list** — `(func (type $late) …) (type $late (func (param
	// i32)))` is legal wat, and this parser accepts it — so at this cursor the number does not exist
	// yet. Locals, meanwhile, resolve *at the cursor* by design (see code.go's header: a deferred local
	// resolution would run against the last function's space), so the two timings are incompatible
	// without the reference's `force_locals` machinery.
	//
	// The bug this replaces was live and the corpus is what found it: `(func (type $sig) (local $var
	// i32) (local.get $var))` encoded `$var` as local **0** where `$sig`'s param owns 0, so `func#2`
	// disagreed with wabt at one byte — 77 bytes each, `20 00` against `20 01`. A well-formed image
	// denoting a different function, which is why refusing is the only honest option short of the full
	// machinery: this stratum's alternative was not "slightly wrong indices", it was silent
	// miscompilation on the commonest typeuse spelling in the corpus.
	//
	// #77 tracked exactly this, on the premise that it had "zero board effect either way" because "this
	// stratum resolves no local at all". The code section is the reader that premise did not have, so
	// the note in typetable.go's header is now historical and the refusal is #77's until #77 lands.
	//
	// It is set from `p.retain` — the *mode* — and not from `p.retaining()`, which asks whether the
	// sink is installed. The sink goes in below, at the body, so `retaining()` is false here in
	// **both** modes and a guard written against it silently never fires: the first draft did exactly
	// that, and the probe showed the offending row still encoding `20 00`. A predicate that is false
	// everywhere it is called is the stillborn control shape (#108) arriving in engine code, and the
	// two spellings are one letter apart.
	if haveUse && ft.isEmpty() && p.retain {
		p.localsMissParams, p.localsMissParamsTok = true, use.tok
		defer func() { p.localsMissParams = false }()
	}
	locals, err := p.locals()
	if err != nil {
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
	//
	// **Kept, by ruling** (Scott, #82), on the same reading that kept `elemField`'s third lookahead:
	// two lines transcribed from two cited productions are a claim about the reference, and the probe
	// above is the honest account of what no test can check. Deleting them because nothing reaches
	// them today is the move #75's shadowing counsels against, one layer out — from a grammar
	// condition into a scope discipline.
	saved := p.ctx.labels.labelReset()
	defer p.ctx.labels.labelRestore(saved)
	p.ctx.labels.labelPushAnon()
	// **The sink is installed here and nowhere else**, which is what makes retention a property of
	// being inside a defined function's body rather than of the instruction readers. Every other caller
	// of `instrList` — a data offset, an elem item, a global initializer — parses instructions this
	// emitter has no section for, and they keep running against a nil sink exactly as before. See
	// code.go's header for why this is a field rather than a parameter threaded through fourteen
	// signatures.
	//
	// **And only when the parse's mode says to.** `p.retain` is read here and nowhere else: a
	// `ReadModule` parse leaves the sink nil through the func body too, so the frontier's refusals
	// stay silent and the recognizer answers exactly what it answered before this file existed. See
	// the field's comment for what installing it unconditionally cost.
	//
	// Swapped-and-restored rather than set-and-cleared, on the same discipline as the label reset two
	// lines up: a func cannot nest in a func in legal wat, so the saved value is always nil today, and
	// writing the swap anyway is what stops that from being an assumption a future caller silently
	// breaks.
	var body instrSink
	outerSink := p.sink
	if p.retain {
		p.sink = &body
	}
	defer func() { p.sink = outerSink }()
	// func_body is instr_list (parser.mly:1019), whose empty arm makes `(func)` well-formed.
	if err := p.instrList(); err != nil {
		return err
	}
	if err := p.rpar(); err != nil {
		return err
	}
	// **After the closing paren, on `memoryField`'s tail's rule**: a field that errors out mid-way
	// must leave no trace, or the retained count disagrees with section 3's on a module that never
	// finished parsing.
	//
	// The locals are stored **unresolved**, and that is not laziness. `resolveVal` resolves a heap
	// type's index against the type space, and `(local (ref null $t))` may name a type defined later in
	// the module — so resolving at the cursor would reject a legal module, which is the accept-direction
	// failure this whole file is careful about. The resolution and the encodability question both belong
	// to the deferred phase, and `encodableOrErr` runs there; see `funcLocalBytes`.
	p.funcs = append(p.funcs, textFunc{typeIdx: typeIdx, locals: locals, kw: kw, body: body})
	p.ctx.noteDefined(importFunc)
	p.ctx.clearNonTypeField(kw)
	return nil
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
// **It returns the resolved type index rather than recording only the check**, which section 3
// needs — `func_section` is `idx x` per function (encode.ml:1141) and `x` is exactly the index the
// helper produced. The value arrives through a captured slot filled by the deferred op, not by
// calling the helper a second time: `inline_functype` *appends*, so a second call for the sugar arm
// would intern a second identical type and the defect would be invisible here, surfacing only as a
// type-section count. That hazard is the one funcField's inline-import comment already names, and
// this is the same slot-plus-thunk shape `defineExport` uses (typetable.go) rather than a second
// answer to it.
//
// The returned thunk must not be called before stage 2. It reports so rather than returning a
// plausible zero, because a zero type index is a *legal* index and a premature read would encode a
// function with the wrong signature and decode clean.
func (p *parser) deferSignature(use idxRef, haveUse bool, ft funcType) func() (uint32, error) {
	var (
		idx    uint32
		filled bool
	)
	if haveUse {
		p.ctx.deferOp(func() error {
			v, err := p.ctx.checkExplicit(use, ft)
			if err != nil {
				return err
			}
			idx, filled = v, true
			return nil
		})
	} else {
		p.ctx.deferOp(func() error {
			v, err := p.ctx.internImplicit(ft)
			if err != nil {
				return err
			}
			idx, filled = v, true
			return nil
		})
	}
	return func() (uint32, error) {
		if !filled {
			return 0, errf(use.tok, "internal: function type index read before stage 2 resolved it")
		}
		return idx, nil
	}
}

// importedFuncDesc is deferSignature for an inline-imported func or tag: same pairing, and the index
// it produces is the descriptor.
//
// **The pairing is written once more, not twice, and this function is the once.** Both inline-import
// arms of `func_fields` (:975-:983) and both of `tag_fields` (:1053-:1065) spend the helper's result
// as `ExternFuncT (Idx y.it)` / `ExternTagT (TagT (Idx y.it))`, and the branch on `haveUse` is
// identical in all four — so the alternative to this function is four sites each choosing a helper,
// which is precisely the failure deferSignature's comment describes: `inline_functype` never fails, so
// a site that interned where the reference compares accepts every mismatched module in silence.
//
// It does **not** call deferSignature. It cannot: deferSignature records a thunk on the deferred list
// itself, whereas this returns a thunk for `defineImport` to record, so that the descriptor lands in
// the import slot. Two functions that look alike because they share a rule, differing in who owns the
// deferral — and the rule they share is in one place, which is the point.
func (p *parser) importedFuncDesc(kind importKind, use idxRef, haveUse bool, ft funcType) func() (importDesc, error) {
	if haveUse {
		return p.externFuncDesc(kind, func() (uint32, error) { return p.ctx.checkExplicit(use, ft) })
	}
	return p.externFuncDesc(kind, func() (uint32, error) { return p.ctx.internImplicit(ft) })
}

// funcSignature parses func_fields_body's params and results (parser.mly:1003-1016), binds the
// params into the local index space, and returns the signature.
//
// Params bind as locals — `anon_locals c (fst $3)` at :1006 and `bind_local c $3` at :1009 — so
// `(func (param $x i32) (local $x i32))` is `duplicate local $x`. That is one of #62's 13
// duplicate-binding vectors and it only works if params and locals share one space.
//
// The locals space is reset per function, since it is `enter_func`'s (parser.mly:965) — and the
// reset carries the kind rather than zeroing it. `space{}` here was the first thing spaceUnset
// caught: a fresh zero value has no category, so every `duplicate local $x` became `duplicate
// <space kind unset> $x`. Which is the marker doing its job, and the reason it renders as
// something no reference message contains — had spaceType sat at ordinal 0 this would have said
// `duplicate type $x` and passed review (grave #120).
//
// It was `markFuncSignature` while the binding was its whole purpose; it returns the type now
// because `func_fields`'s arms pass `fst $2 c'` — this signature — to whichever helper applies.
// Renamed rather than given a second name: two functions reading the same production is the drift
// shape, and the *reason* the name changed is that the production always returned this and the
// previous stratum had nothing to do with it.
func (p *parser) funcSignature() (funcType, error) {
	p.ctx.locals = space{kind: spaceLocal}
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
			if err := p.ctx.locals.bindAbs(tok, name); err != nil {
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
//
// It returns the declared valtypes in order, which is what the code section's locals vector is.
// They were previously read and discarded on the true premise that *the grammar* never compares a
// local's type — the comment saying so is quoted in the body below, because the premise is still
// true and is no longer sufficient: retaining them is the code section's requirement, not the
// grammar's.
func (p *parser) locals() ([]valType, error) {
	var out []valType
	for p.c.at(LParen) && p.c.peek2Keyword(kwLocal) {
		if err := p.lpar(kwLocal); err != nil {
			return nil, err
		}
		if p.c.at(VarTok) {
			tok := p.c.peek()
			name, err := p.bindidx()
			if err != nil {
				return nil, err
			}
			if berr := p.ctx.locals.bindAbs(tok, name); berr != nil {
				return nil, berr
			}
			// The named arm binds exactly one local (`local_type` is singular at :1006), so the
			// valtype is appended once. It used to be discarded here — "a local's type is nothing
			// the grammar compares. Only a functype's value types reach
			// `inline_functype_explicit`" — which remains an accurate statement about the
			// *grammar* and is why nothing was lost before section 10 existed.
			v, err := p.valtype()
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		} else {
			vs, err := p.valtypeList()
			if err != nil {
				return nil, err
			}
			for range vs {
				p.ctx.locals.bindAnon()
			}
			// **The binding count and the type count are the same number, and that is the
			// invariant this arm rests on**: `bindAnon` is called once per valtype, so a local's
			// index is its position in this list. If the two ever diverged, `local.get 3` would
			// resolve against one vector and be typed against another, which validation would
			// catch on a well-formed module and would silently mistype on a malformed one.
			out = append(out, vs...)
		}
		if err := p.rpar(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// tagField parses `tag` (parser.mly:1042-1072).
//
// Its four non-export arms (:1047-1067) pair the two helpers exactly as `func_fields` does — a
// typeuse present means `inline_functype_explicit`, absent means `inline_functype` — and unlike
// `func` there is no body arm, so the `functype` is always present in the source. Note the
// signature is read by `functype` in *both* arms, import or not: a tag has no locals, so nothing
// here binds.
func (p *parser) tagField() error {
	kw := p.c.peek2()
	if err := p.lpar(kwTag); err != nil {
		return err
	}
	idx, idxErr := p.bindidxOpt(&p.ctx.tags)
	if idxErr != nil {
		return idxErr
	}
	if err := p.inlineExports(importTag, idx); err != nil {
		return err
	}
	imp, err := p.inlineImport()
	if err != nil {
		return err
	}
	use, haveUse := idxRef{}, false
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
	if imp.have {
		// See funcField's arm for why this *replaces* deferSignature rather than joining it.
		return p.inlineImportTail(imp, kw, p.importedFuncDesc(importTag, use, haveUse, ft))
	}
	p.deferSignature(use, haveUse, ft)
	p.ctx.markDefined(importTag)
	return p.rpar()
}

// globalField parses `global` (parser.mly:1074-1089).
//
// The non-import arm is `globaltype constexpr`, and constexpr is an instruction sequence — so this
// was the second place the old stratum boundary showed. Both arms now complete: measured, not
// assumed, since a sentence about which of two spellings parses is the kind that goes stale
// silently.
//
// **Both arms now *encode*, which is section 6 landing (#8).** The defining arm's initializer is read
// into its own sink rather than into the enclosing one, for `elemexprRetained`'s reason: a const
// expression is a self-contained `const c` with its own terminator (encode.ml:993), so instructions
// belonging to one global must not run together with a neighbour's. `intoSink` gates on the *mode*
// rather than on whether a sink is installed, which is grave #144 — and it matters here for the same
// reason it mattered at module-field scope then: nothing installs a sink at this level, so asking
// `retaining()` would return an empty sink on a parse that is retaining and emit `(global i32
// (i32.const 7))` as a bare `0x0b` — a global with no initializer, decoding clean.
func (p *parser) globalField() error {
	kw := p.c.peek2()
	if err := p.lpar(kwGlobal); err != nil {
		return err
	}
	idx, idxErr := p.bindidxOpt(&p.ctx.globals)
	if idxErr != nil {
		return idxErr
	}
	// Before the arms, as in memoryField: the index is the field's own whichever arm follows, and an
	// inline export on an *imported* global is an export of that import's index.
	if err := p.inlineExports(importGlobal, idx); err != nil {
		return err
	}
	imp, err := p.inlineImport()
	if err != nil {
		return err
	}
	gt, fill, err := p.importedGlobal()
	if err != nil {
		return err
	}
	if imp.have {
		return p.inlineImportTail(imp, kw, fill)
	}
	// The defining arm. The *type* came from the same helper both arms use, which is the reference's
	// shape too (`globaltype` appears at both :1080 and :1082) — only the thunk is the import's, and it
	// goes unused here.
	p.ctx.markDefined(importGlobal)
	// constexpr is instr_list (parser.mly:951), so `(global i32)` with no initializer is
	// well-formed *grammatically* — the arity is validation's complaint, not the parser's. It encodes
	// to a bare `0x0b`, which is what the reference writes for the same input.
	init, err := p.intoSink(p.instrList)
	if err != nil {
		return err
	}
	if err := p.rpar(); err != nil {
		return err
	}
	// **After the closing paren**, on `memoryField`'s tail's rule: a field that errors out mid-way must
	// leave no trace, or section 6's count disagrees with the grammar's on a module that never finished
	// parsing.
	p.ctx.defineGlobal(textGlobal{typ: gt, init: init})
	p.ctx.noteDefined(importGlobal)
	p.ctx.clearNonTypeField(kw)
	return nil
}

// importedExternType parses an inline import's type and closes the field.
//
// **A three-line helper, and the reason it exists is worth more than the three lines.** Inline in
// `memoryField` and `tableField`, this arm sat inside the live range of the outer `err` that the
// retention below now reads — so `govet`'s `shadow` objected to `if err := …` and `gocritic`'s
// `sloppyReassign` objected to `if err = …`. Two curated linters wanting opposite things is not noise
// to suppress; it is both of them describing one fact, which is that an error was being held live
// across an arm that has no business in it. Lifting the arm out ends the disagreement by removing its
// subject, and that is the outcome the spirit clause asks for — a `nolint` on either would have
// recorded the standoff and fixed nothing.
//
// It also unifies what was two copies of one three-line sequence, and both call sites are the same
// fact: an imported memory or table is an `Import`, not a definition, so its type belongs to a section
// this emitter does not write and nothing here retains it.
//
// The first attempt at this got it wrong in a way worth recording: it kept the arms in place, wrote
// `err =`, and asserted in a comment that `sloppyReassign` "does not fire on this one because the
// variable genuinely is reused". It fires. The claim was checked by running the linter rather than by
// reading it, which is the only reason it did not ship as a comment stating the opposite of the truth.
//
// **It retains now, and its old comment's own words are why the change is more than a signature.**
// Two paragraphs up: "an imported memory or table is an `Import`, not a definition, so its type
// belongs to a section this emitter does not write and nothing here retains it." That section is what
// this PR writes, so the second clause is what expired — the first is as true as ever, and is exactly
// what makes the import list the right home for these two.
//
// The four steps are in this order at all three call sites and the order is load-bearing at two of
// them: the paren is consumed **before** the retention, so a field that errors out mid-way leaves no
// trace and `encodableOrErr`'s count check cannot disagree with the sections on a module that never
// finished parsing. That is memoryField's tail's rule, and having one helper is how the third site
// gets it without anyone having to remember.
//
// **The `exported bool` parameter is gone, and its disappearance is the export section landing.**
// It existed to suppress the frontier withdrawal — an inline export made a field unencodable, so the
// field could not clear its own refusal. Now `inlineExports` retains, so the withdrawal is
// unconditional and the five call sites that computed `exported` have nothing left to compute. It is
// removed rather than left as an always-false argument, because a parameter no caller can vary is a
// branch no test can reach.
func (p *parser) inlineImportTail(imp inlineImportNames, kw Token, fill func() (importDesc, error)) error {
	if err := p.rpar(); err != nil {
		return err
	}
	p.ctx.defineImport(imp.module, imp.name, fill)
	p.ctx.clearNonTypeField(kw)
	return nil
}

// memoryField parses `memory` (parser.mly:1112-1134).
//
// Four arms, one of them the `addrtype (data string_list)` sugar that defines a memory sized to
// its own data — and that arm creates a *data segment* too, which is why the reference threads a
// data list out of memory_fields. Under 0011 nothing is created, but the arm still has to parse
// or `(memory (data "abc"))` is a valid module rejected.
// **Three of its four arms are encodable (#8), and the `(data …)` sugar is the one that is not.** It
// defines both a memory *and* a data segment, and emitting the memory without the data would be a
// module that decodes clean and means something else. The plain `addrtype limits` arm withdraws the
// frontier at the end, where every retention has happened; the inline-import arm withdraws through
// `inlineImportTail`, section 2 having landed in #119.
//
// This paragraph read "**one** of its four arms is encodable … an inline export needs an export
// section", which counted the inline export as a fifth arm's worth of refusal and was true until this
// PR. It is now wrong twice over: the inline export is a *prefix* rather than an arm — it precedes the
// arm split, which is why `inlineExports` is called above the `imp.have` branch — and section 7
// exists, so it withdraws nothing. The count is quoted here rather than left vague because a
// paragraph naming a number is checkable against the arms and a paragraph saying "some" is not.
func (p *parser) memoryField() error {
	kw := p.c.peek2()
	if err := p.lpar(kwMemory); err != nil {
		return err
	}
	idx, idxErr := p.bindidxOpt(&p.ctx.memories)
	if idxErr != nil {
		return idxErr
	}
	// Before the arm is known, because the index does not depend on the arm: an imported memory
	// occupies an index in the memory space exactly as a defined one does.
	if err := p.inlineExports(importMemory, idx); err != nil {
		return err
	}
	imp, err := p.inlineImport()
	if err != nil {
		return err
	}
	if imp.have {
		fill, ftErr := p.importedMemory()
		if ftErr != nil {
			return ftErr
		}
		return p.inlineImportTail(imp, kw, fill)
	}
	// The `addrtype LPAR DATA string_list RPAR` sugar (parser.mly:1129), distinguished from
	// `memorytype` by an LPAR where a nat would be. addrtype is optional in both, so it is
	// consumed first and the branch happens after.
	addr64, err := p.addrtype()
	if err != nil {
		return err
	}
	if p.c.at(LParen) && p.c.peek2Keyword(kwData) {
		// `idx` is this memory's own index, which the sugar's data segment is `Active` on — not a
		// defaulted 0. See memoryDataSugar.
		return p.memoryDataSugar(kw, idx, addr64)
	}
	lim, err := p.limits()
	if err != nil {
		return err
	}
	p.ctx.markDefined(importMemory)
	if err := p.rpar(); err != nil {
		return err
	}
	// After the closing paren, so a malformed field never records content. The order matters for the
	// retention check: a field that errors out mid-way must leave no trace, or the counts disagree
	// with the sections on a module that never finished parsing.
	p.ctx.defineMemory(memType{addr64: addr64, lim: lim})
	p.ctx.noteDefined(importMemory)
	p.ctx.clearNonTypeField(kw)
	return nil
}

// memoryDataSugar parses the tail of `(memory <addrtype> (data …))` (parser.mly:1129).
//
// **Its own function for a lint reason that turned out to be a structural one.** Inline, it put three
// `if err := …` blocks between `memoryField`'s outer `err` and that variable's last use, so `govet`'s
// `shadow` reported each — and rewriting them as `err =` traded those for `gocritic`'s
// `sloppyReassign`, the two linters wanting opposite things. Neither is wrong: the conflict is the
// signal that one function was holding an error live across an arm that does not need it. Extracting
// the arm satisfies both by removing the cause, which is the outcome the spirit clause prefers over a
// suppression — a `nolint` here would have documented a disagreement instead of resolving it.
//
// **The arm defines two things and now retains both** (#8). The paragraph that stood here read "no
// retention and no withdrawal: this arm defines a memory the emitter *cannot* write, because its size
// is derived from a data segment there is no data section for yet" — honest while section 11 did not
// exist, and section 11 is what expired it. It is quoted rather than deleted because the reason for
// the decline is the reason this arm is *interesting*: it is the one place a memory's own type is
// computed from a data segment's length, so retaining one without the other would emit a memory of
// the wrong size or a segment with no home.
//
// The size is the reference's own arithmetic, `Int64.(div (add (of_int (String.length $4)) 65535L)
// 65536L)` (parser.mly:1128) — the payload rounded **up** to whole 64KiB pages — with min and max
// both set to it. Ceiling rather than floor: a floor would size `(memory (data "x"))` at zero pages
// and the segment would not fit in the memory it was written for, which validation catches and the
// encoder should never produce. The offset is `at_const $1 (0L)` (:1130, mnemonics.ml:18-20), an
// `i32.const 0` or `i64.const 0` by the memory's own address type — so a 64-bit memory's sugar
// segment gets an `i64.const`, and writing `i32.const` there would encode an offset of the wrong
// type in an image that decodes clean.
//
// The data segment's memory index is **this memory's own**, `Active (x, offset)` where `x` is
// `bindidx_opt`'s value — not a defaulted 0. For `(module (memory 1) (memory (data "x")))` the sugar
// segment belongs to memory *1*, and defaulting it would silently write the bytes into the first
// memory.
func (p *parser) memoryDataSugar(kw Token, idx uint32, addr64 bool) error {
	if err := p.lpar(kwData); err != nil {
		return err
	}
	p.ctx.noteData()
	bs := p.stringList()
	if err := p.rpar(); err != nil {
		return err
	}
	p.ctx.datas.bindAnon() // the sugar's implicit data segment
	p.ctx.markDefined(importMemory)
	if err := p.rpar(); err != nil {
		return err
	}
	// After the closing paren, on `memoryField`'s tail's rule, and in the reference's own order:
	// the memory first, then the data segment that sizes it.
	pages := (uint64(len(bs)) + 0xFFFF) / 0x10000
	p.ctx.defineMemory(memType{addr64: addr64, lim: limits{min: pages, max: pages, hasMax: true}})
	p.ctx.noteDefined(importMemory)
	p.ctx.defineData(textData{
		mem:    idxRef{idx: idx},
		offset: sugarZeroOffset(addr64),
		bytes:  bs,
	})
	p.ctx.clearNonTypeField(kw)
	return nil
}

// sugarZeroOffset is the `at_const <addrtype> 0` offset the two data-bearing sugar arms synthesize
// (parser.mly:1130, mnemonics.ml:18-20).
//
// **The instruction has no source token, which is why it is built here rather than parsed.** It is
// the reference's `[at_const $1 (0L @@ loc) @@ loc]` — a one-instruction const expression the text
// never spells — and the address type selects the mnemonic: `I32AT -> i32_const`, `I64AT ->
// i64_const`. A `(memory i64 (data "x"))` whose offset were written as `i32.const 0` would encode an
// i32 offset for an i64 memory, which is a validation error the encoder has no business producing.
//
// The immediate is a *signed* LEB of zero, which is one `00` byte either way — the same byte an
// `i32.const 0` from source text gets, because `constImmBytes` writes `s32`/`s64` and both are
// minimal. So the two paths agree by construction rather than by coincidence.
func sugarZeroOffset(addr64 bool) instrSink {
	mnemonic := "i32.const"
	if addr64 {
		mnemonic = "i64.const"
	}
	op, ok := opBytes(mnemonic)
	if !ok {
		// Unreachable: both mnemonics are in the generated table, which
		// TestSugarZeroOffsetEncodesTheAddressTypesConst pins by decoding what this produces. A panic
		// rather than an empty sink, because an offset expression missing its instruction encodes a
		// segment whose offset is whatever the stack happened to hold — grave #36's class in an image.
		panic("text: " + mnemonic + " has no opcode, so the data sugar's synthesized offset cannot " +
			"be built")
	}
	var s instrSink
	s.add(instr{op: op, imm: []byte{0x00}})
	return s
}

// tableField parses `table` (parser.mly:1185-1225).
//
// Five arms. Two are sugar forms taking `(elem …)` and sizing the table to it, and both create
// an elem segment. The `tabletype constexpr1` arm reaches the instruction boundary; the bare
// `tabletype` arm (`:1192`) completes, because the reference synthesizes a `ref.null` init.
// **Two of its five arms are encodable (#8): `tabletype` with no initializer**, the bare form at
// :1192, and the inline-import arm since section 2 landed in #119. The `constexpr1` arm has an
// initializer expression the emitter cannot write (no instruction emitter yet), and both `(elem …)`
// sugar arms define an elem segment there is no elem section for. An inline export is not among the
// refusals and never was an arm — see memoryField, which owns the paragraph correcting that count.
func (p *parser) tableField() error {
	kw := p.c.peek2()
	if err := p.lpar(kwTable); err != nil {
		return err
	}
	idx, idxErr := p.bindidxOpt(&p.ctx.tables)
	if idxErr != nil {
		return idxErr
	}
	if err := p.inlineExports(importTable, idx); err != nil {
		return err
	}
	imp, err := p.inlineImport()
	if err != nil {
		return err
	}
	if imp.have {
		fill, ttErr := p.importedTable()
		if ttErr != nil {
			return ttErr
		}
		return p.inlineImportTail(imp, kw, fill)
	}
	addr64, err := p.addrtype()
	if err != nil {
		return err
	}
	// `addrtype reftype (elem …)` sugar (parser.mly:1205/1216) versus `addrtype limits
	// reftype`: the sugar has no limits, so a reftype where a nat would be is the tell.
	if p.atReftypeStart() {
		return p.tableElemSugar(kw, idx, addr64)
	}
	lim, err := p.limits()
	if err != nil {
		return err
	}
	elem, err := p.reftype()
	if err != nil {
		return err
	}
	p.ctx.markDefined(importTable)
	if p.c.at(RParen) { // the bare-tabletype arm, parser.mly:1192
		if err := p.rpar(); err != nil {
			return err
		}
		// The reference synthesizes a `ref.null ht` initializer for this arm (:1193-1194), and
		// `encode.ml:960-962` writes the *plain* tabletype whenever the initializer is exactly
		// `ref.null` of the table's own heap type — which is precisely this arm. So the bare form
		// round-trips with no initializer to write, and the 0x40 form is never needed here.
		p.ctx.defineTable(tabType{addr64: addr64, lim: lim, elem: elem})
		p.ctx.noteDefined(importTable)
		p.ctx.clearNonTypeField(kw)
		return nil
	}
	if err := p.constexpr1(); err != nil { // tabletype constexpr1
		return err
	}
	return p.rpar()
}

// tableElemSugar parses the tail of `(table <addrtype> reftype (elem …))` (parser.mly:1205/1216).
//
// Extracted for the reason `memoryDataSugar` records: inline, its nested arms held `tableField`'s
// outer `err` live and pitted `shadow` against `sloppyReassign`.
//
// **Both halves are retained now (#8, 0016), where this comment used to say "nothing retained: this
// arm's table is sized from an elem segment the emitter cannot write".** The table and the segment come
// out of one field, and the reason they cannot be split is arithmetic: the table's limits are
// `min = max = len(einit)` (parser.mly:1216-1222), so retaining the table without the segment writes a
// table whose size was computed from elements that went nowhere — a table of two nulls where the text
// said two functions, decoding clean. That module is what this arm emitted for as long as the frontier
// held it back, and the section-9 withdrawal check in `encodableOrErr` is the control that would have
// caught it: this arm is the reason that check is live rather than a tripwire.
//
// **The reference resolves the reftype here too** — the arm is `$2 c`, a lookup — so
// `(table (ref null $undefined) (elem))` is a module we accept and upstream rejects. Declared and
// tracked as **#111**, which measures every accepting site and holds the scope-to-the-space requirement
// for the control: fixed here, it would be one site of one shape repaired inside a PR about section
// emitters, with the rest left silently green. The issue's table was stale by four rows from the import
// section until the export section PR re-ran the probe and corrected it there — `importedGlobal` and
// `importedTable` resolve, across both spellings each — and this arm is one of the six that still
// accept.
//
// The two arms differ in the element list's form *and* in the element type, and the second difference is
// not a detail: the `elemidx_list` arm states `let rt = (NoNull, FuncHT)` in its own action (:1217)
// where the `elemexpr` arm takes `$2 c`, the reftype the text wrote. So `(table funcref (elem 0))` and
// `(table funcref (elem (ref.func 0)))` have *different* element types — `(NoNull, FuncHT)` and
// `(Null, FuncHT)` — and take flags 0x00 and 0x04. The table's own `RefType` is `$2 c` in both.
func (p *parser) tableElemSugar(kw Token, idx uint32, addr64 bool) error {
	rt, err := p.reftype()
	if err != nil {
		return err
	}
	if err := p.lpar(kwElem); err != nil {
		return err
	}
	p.ctx.noteElem()
	// Both sugar arms' contents are instruction-level: elemexpr_list or elemidx_list. The
	// idx list is reachable here; an `(item …)` or folded expr is #63's — the folded arm
	// by the defect-ownership ruling on #63, which put `expr1`'s minimal arm there.
	//
	// **Each arm carries its own error and the reftype's is dead by here**, which is
	// `importedExternType`'s standoff avoided rather than met: reusing the outer `err` across
	// these arms is what makes `govet`'s `shadow` and `gocritic`'s `sloppyReassign` want
	// opposite things, and the two of them are describing one fact — an error held live across
	// arms that have no business in it.
	var (
		elems    []instrSink
		elemType = rt
	)
	if p.c.at(NatTok) || p.c.at(VarTok) || p.c.at(RParen) {
		// elemidx_list (parser.mly:1147), whose idx_list has an empty arm — so
		// `(table funcref (elem))` is well-formed. The element type is the action's own
		// `(NoNull, FuncHT)`, not the reftype the table was given.
		list, err := p.elemIdxList()
		if err != nil {
			return err
		}
		elems, elemType = list, elemKindFuncref
	} else {
		list, err := p.elemexprListRetained() // parser.mly:1205
		if err != nil {
			return err
		}
		elems = list
	}
	if err := p.rpar(); err != nil {
		return err
	}
	p.ctx.elems.bindAnon() // the sugar's implicit elem segment
	p.ctx.markDefined(importTable)
	if err := p.rpar(); err != nil {
		return err
	}
	// After the closing paren, on `memoryDataSugar`'s rule, and in the reference's own order: the table
	// first, then the segment that sizes it.
	n := uint64(len(elems))
	p.ctx.defineTable(tabType{addr64: addr64, lim: limits{min: n, max: n, hasMax: true}, elem: rt})
	p.ctx.noteDefined(importTable)
	p.ctx.defineElem(textElem{
		table:    idxRef{idx: idx},
		offset:   sugarZeroOffset(addr64),
		elemType: elemType,
		elems:    elems,
	})
	p.ctx.clearNonTypeField(kw)
	return nil
}

// dataField parses `data` (parser.mly:1095-1107) and retains it as section 11's content (#8).
//
// Three arms, and all three are now encodable: passive, `(memory idx) offset string_list`, and the
// offset-only sugar. The paragraph that stood here said "only the passive arm completes in this
// stratum — which is the entire reason `(data "\ef\ff\fe")`'s legality is provable here at all", and
// the legality half is still why the payload has no UTF-8 check; the arm half expired with this PR.
//
// **The offset is a constant expression in a module field, which is where the instruction sink
// leaves `funcField` for the first time.** That escape is the grammar's shape rather than scope
// creep: `offset` is `LPAR OFFSET constexpr RPAR | expr` (parser.mly:1091-1093) and the two active
// arms both take one, so a data section cannot be written without retaining instructions outside a
// function body. The reference does exactly this — the offset arms at :1102/:1105 build const
// exprs that `module_fields1`'s second closure resolves — so the sink following the grammar out of
// the func body is consumer-forced retention under 0006's rule, not a widening of it. See
// `retainedOffset` for the swap-and-restore, whose falsification is
// TestRetainedOffsetRestoresTheOuterSink.
func (p *parser) dataField() error {
	kw := p.c.peek2()
	if err := p.lpar(kwData); err != nil {
		return err
	}
	p.ctx.noteData()
	if _, err := p.bindidxOpt(&p.ctx.datas); err != nil {
		return err
	}
	if p.c.at(LParen) && p.c.peek2Keyword(kwMemory) {
		mem, err := p.memoryUse()
		if err != nil {
			return err
		}
		off, err := p.retainedOffset()
		if err != nil {
			return err
		}
		bs := p.stringList()
		if err := p.rpar(); err != nil {
			return err
		}
		// After the closing paren, on `memoryField`'s tail's rule: a field that errors out mid-way
		// must leave no trace, or the retained segment count disagrees with section 11's on a module
		// that never finished parsing.
		p.ctx.defineData(textData{mem: mem, offset: off, bytes: bs})
		p.ctx.clearNonTypeField(kw)
		return nil
	}
	if p.c.at(LParen) {
		// The offset sugar: `(offset …)` or a folded expr (parser.mly:1103-1105). The memory index
		// **defaults to 0** — `Active (0l @@ $sloc, $4 c)` — which is `textData.mem`'s zero value, an
		// `idxRef` resolving to 0. Not a separate flag: `data`'s `0x00` and `0x02` arms split on the
		// *resolved* index, so an explicit `(memory 0)` and this default are the same encoding, and
		// `textData`'s comment has why the first draft's flag was wrong.
		off, err := p.retainedOffset()
		if err != nil {
			return err
		}
		bs := p.stringList()
		if err := p.rpar(); err != nil {
			return err
		}
		p.ctx.defineData(textData{offset: off, bytes: bs})
		p.ctx.clearNonTypeField(kw)
		return nil
	}
	bs := p.stringList()
	if err := p.rpar(); err != nil {
		return err
	}
	// Passive: no offset at all, which `passive` says rather than a nil-offset test. An empty
	// instruction list is a legal *active* offset (`(data (offset) "")` — `constexpr` is `instr_list`,
	// so the empty sequence parses), so nil-ness cannot carry the mode.
	p.ctx.defineData(textData{passive: true, bytes: bs})
	p.ctx.clearNonTypeField(kw)
	return nil
}

// memoryUse parses `memory_use` (parser.mly:1108): `LPAR MEMORY var RPAR`.
//
// Its own function rather than four inlined lines in dataField, and the reason is the grammar's
// rather than style's: `memory_use` is a named production, and `data`'s active arm is the first of
// what will be several consumers (`elem`'s `table_use` is its sibling, and #63's `memory.init`
// takes the same shape). Naming it here is where the second consumer looks.
func (p *parser) memoryUse() (idxRef, error) {
	if err := p.lpar(kwMemory); err != nil {
		return idxRef{}, err
	}
	mem, err := p.idxValue()
	if err != nil {
		return idxRef{}, err
	}
	if err := p.rpar(); err != nil {
		return idxRef{}, err
	}
	return mem, nil
}

// tableUse parses `table_use` (parser.mly:1182): `LPAR TABLE var RPAR`.
//
// **The second consumer memoryUse's comment named, arriving.** That comment said "`elem`'s
// `table_use` is its sibling, and #63's `memory.init` takes the same shape" — so this is not a
// speculative extraction, it is the sibling the note was left for. Same four lines, one keyword
// apart, and the two are deliberately *not* one parameterized helper: `memory_use` and `table_use`
// are separate named productions in the reference, and collapsing them would put a keyword argument
// where the grammar has two rules.
//
// It exists as a function rather than inline in elemField's tableuse arm for a second reason, which
// is what forced it out of that arm: inlined, its `rpar` sat inside the live range of the outer
// `err` that the retention below reads, so `govet`'s `shadow` wanted `err =` and `gocritic`'s
// `sloppyReassign` wanted `err :=`. That standoff is `importedExternType`'s, verbatim — two curated
// linters describing one fact, an error held live across an arm with no business in it — and the
// ruling there was to remove the subject rather than suppress either linter.
func (p *parser) tableUse() (idxRef, error) {
	if err := p.lpar(kwTable); err != nil {
		return idxRef{}, err
	}
	tab, err := p.idxValue()
	if err != nil {
		return idxRef{}, err
	}
	if err := p.rpar(); err != nil {
		return idxRef{}, err
	}
	return tab, nil
}

// retainedOffset parses a segment's offset, retaining the instructions it holds.
//
// **This is the one place the instruction sink is installed outside a function body**, and the escape
// is grammar-forced: section 11's own grammar puts a constant expression in a module field
// (parser.mly:1102/1105, `offset` at :1091-1093), and `encode.ml`'s `data` writes `const c` for both
// active arms (:1098/:1101). A data section cannot be emitted without it. That is 0006's rule
// running normally rather than an exception to it — the retention is grown from what this section's
// grammar requires and no wider — and the reference resolves these same const exprs in
// `module_fields1`'s second closure, so the shape is transcribed rather than invented.
//
// **It was `dataOffset`, and section 9 is what expired the name rather than a preference about
// naming.** An element segment's active arms take the *same* `offset` production and want the same
// retention, so the second consumer arrived reading identically to the first — and the shape this
// project has a grave for (#78/#105) is the one where that reads as a fact about the caller and gets
// re-derived next door. The first draft of the elem wiring did exactly that: a verbatim copy named
// `elemOffset`, which is *one concept, one trigger* (#82) broken in the same session as the rule was
// quoted. What makes one reader correct here is that nothing in the body was ever about data —
// `offset` is one production, and the swap is about being at module-field level, which both callers
// are.
//
// The swap-and-restore is `funcField`'s, copied rather than re-derived (*lessons are indexed by
// shape*). **The saved value is always nil, and this comment said otherwise until it was measured.**
// The claim was "genuinely non-nil in one case", which reads like the interesting half of the
// mechanism; a counter on both branches, over all four `(data …)` spellings and the memory sugar,
// reports `nil=1 nonNil=0` on every one — a `(data …)` field cannot nest inside a func body in any
// spelling. So the swap's job here is the **clear**, exactly as it is in `funcField`, and writing it
// as a swap is what keeps the nil-ness a fact about the grammar rather than an assumption a future
// caller silently breaks.
//
// The correction matters because a control written to the old claim would have hunted for a non-nil
// outer sink, found no input producing one, and been stillborn. Falsified as
// TestRetainedOffsetRestoresTheOuterSink instead, in the direction that exists: deleting the restore
// leaks the offset's `i32.const 0` into module-field scope, and the visible symptom is a *later*
// field refusing at the wrong layer — `(table 1 funcref (ref.null func))` reporting the ref.null
// instruction where it must report the `(table …)` field.
//
// **Only when the mode says to retain**, on `funcField`'s argument: a `ReadModule` parse must leave
// the sink nil so `refuseUnencodable` stays silent and the recognizer answers exactly what it
// answered before section 11 existed.
func (p *parser) retainedOffset() (instrSink, error) {
	var off instrSink
	outerSink := p.sink
	if p.retain {
		p.sink = &off
	}
	defer func() { p.sink = outerSink }()
	if err := p.offset(); err != nil {
		return instrSink{}, err
	}
	return off, nil
}

// elemField parses `elem` (parser.mly:1158-1180).
//
// **All five arms are now retained and encodable (#8, 0016)**, where the paragraph here used to say
// that "every non-passive one holds an offset or an elemexpr, so this stratum reaches the passive
// `elemkind elemidx_list` arm and the declarative arm, and stops at the rest". The offsets and the
// element expressions are what changed: `retainedOffset` keeps the first and `elemexprRetained` keeps
// the second, one sink per element.
//
// The arms differ in exactly three things — the **mode**, the **table index**, and whether there is
// an offset — and they share the element list, which is why the tail is one call and not five. The
// mode is what the reference's three `segmentmode` values carry (`Passive`, `Active`, `Declarative`),
// and none of the five arms is a fourth mode: two of them are *sugar for* `Active` with table 0.
func (p *parser) elemField() error {
	kw := p.c.peek2()
	if err := p.lpar(kwElem); err != nil {
		return err
	}
	if _, err := p.bindidxOpt(&p.ctx.elems); err != nil {
		return err
	}
	p.ctx.noteElem()
	if p.c.atKeyword(kwDeclare) {
		p.c.next()
		e, err := p.elemListRetained()
		if err != nil {
			return err
		}
		if err := p.rpar(); err != nil {
			return err
		}
		// **After the closing paren**, on `memoryField`'s tail's rule: a field that errors out
		// mid-way must leave no trace, or section 9's count disagrees with the grammar's on a module
		// that never finished parsing.
		e.mode = elemDeclarative
		p.ctx.defineElem(e)
		p.ctx.clearNonTypeField(kw)
		return nil
	} else if p.c.at(LParen) && p.c.peek2Keyword(kwTable) { // tableuse, parser.mly:1182
		tab, err := p.tableUse()
		if err != nil {
			return err
		}
		off, err := p.retainedOffset() // parser.mly:1164
		if err != nil {
			return err
		}
		e, err := p.elemListRetained()
		if err != nil {
			return err
		}
		if err := p.rpar(); err != nil {
			return err
		}
		e.table, e.offset = tab, off
		p.ctx.defineElem(e)
		p.ctx.clearNonTypeField(kw)
		return nil
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
		// silent*.
		//
		// **Kept, by ruling** (Scott, #82). Deleting a condition on the strength of "nothing reaches
		// it today" is precisely the move the elemexpr arm's own shadowing (#75) counsels against: a
		// future arm could make the position reachable, and the deletion would be invisible until it
		// did. The measurement above stays as the record of what this condition does *not* do, which
		// is what makes keeping it a statement rather than a habit.
		off, err := p.retainedOffset()
		if err != nil {
			return err
		}
		e, err := p.offsetSugarElems()
		if err != nil {
			return err
		}
		if err := p.rpar(); err != nil {
			return err
		}
		e.offset = off
		p.ctx.defineElem(e)
		p.ctx.clearNonTypeField(kw)
		return nil
	}
	// The passive arm (parser.mly:1159), which is the one with no offset and no table.
	e, err := p.elemListRetained()
	if err != nil {
		return err
	}
	if err := p.rpar(); err != nil {
		return err
	}
	e.mode = elemPassive
	p.ctx.defineElem(e)
	p.ctx.clearNonTypeField(kw)
	return nil
}

// offsetSugarElems parses what follows the offset in `elem`'s offset-sugar arms: either
// `elemidx_list` (parser.mly:1175) or `elem_list` (:1171).
//
// **The two sub-arms differ only in the element list, so the tail is the caller's** — the offset,
// the closing paren, and the `defineElem` are one sequence in `elemField` rather than duplicated per
// sub-arm, which is `elemListRetained`'s own argument applied one level down.
//
// It is a function rather than two inlined branches because inlined they sat inside the live range
// of the offset's `err`: `govet`'s `shadow` wanted `err =` and `gocritic`'s `sloppyReassign` wanted
// `err :=`, which is `importedExternType`'s standoff exactly. Two curated linters wanting opposite
// things is one fact about an error held live across an arm with no business in it, and the ruling
// there was to remove the subject rather than suppress either.
//
// `elemidx_list` (:1147) is the second sugar arm and is just `idx_list`, so a bare index list after
// the offset is legal and needs no elemkind.
//
// **Its element type is `(NoNull, FuncHT)` and this arm is the reason that is not an implementation
// detail.** The reference's action states it outright — `let rt = (NoNull, FuncHT) in` (:1177) —
// which is `is_elem_kind`, so `(elem (i32.const 0) $f)` takes flag **0** while the visually similar
// `(elem (i32.const 0) funcref (ref.func $f))` takes flag 4. Two arms, two reftypes, two encodings,
// and only the reftype says which.
//
// **`RParen` is in the condition because `idx_list` has an empty arm (parser.mly:500), and this is
// the *only* arm of the five that reaches an empty element list** — `elem_list`'s two arms both start
// with a mandatory token, so `(elem (i32.const 0))` is well-formed while `(elem)` is not (#143). 29
// corpus rows take this route, `elem.wast:35` and `:39` among them, every one of them an offset with
// nothing after it. Note the sibling: `tableElemSugar` had this condition right from the start, in
// the same file, for the same reason about the same production — the empty case was mis-sited as an
// `elem_list` arm and only reading the reference's `idx_list` said which level owns it.
func (p *parser) offsetSugarElems() (textElem, error) {
	if p.c.at(NatTok) || p.c.at(VarTok) || p.c.at(RParen) {
		elems, err := p.elemIdxList()
		if err != nil {
			return textElem{}, err
		}
		return textElem{elemType: elemKindFuncref, elems: elems}, nil
	}
	return p.elemListRetained()
}

// elemListRetained parses `elem_list` (parser.mly:1152-1156) and returns the segment it denotes: its
// element type and its element expressions.
//
// Split out of elemField because four of the five `elem` arms end with one, and inlining it four
// times is how the arms drift apart. There was a recognizer twin, `elemList`, returning bare `error`;
// once every arm retained, nothing called it and `deadcode` said so.
//
// Split as a value-returning twin rather than by threading state through the recognizer, on
// `idxValue`/`idx`'s shape — see `elemexprListRetained`, whose argument this shares.
//
// **A `textElem` rather than a `(valType, []instrSink)` pair, and the reason is the caller count.**
// Four of `elemField`'s five arms end with one of these and each then sets one or two more fields —
// the mode, the table, the offset — so returning the struct lets every arm read as "the list, plus
// what my arm adds", which is exactly what the reference's arms are: `fun () -> let rt, cs = $4 c in
// Elem (rt, cs, Passive)` and its three siblings differ only in the third argument (parser.mly:1158-
// 1180).
//
// The two arms are where the element *type* comes from, and it decides the wire form rather than
// decorating it — `elemKindFuncref` versus whatever `reftype` read. See `textElem` for the derivation
// and for the two directions in which remembering the spelling instead would mis-encode.
func (p *parser) elemListRetained() (textElem, error) {
	if p.c.atKeyword(kwFunc) { // elemkind, parser.mly:1136
		p.c.next()
		elems, err := p.elemIdxList()
		if err != nil {
			return textElem{}, err
		}
		return textElem{elemType: elemKindFuncref, elems: elems}, nil
	}
	if p.atReftypeStart() {
		rt, err := p.reftype()
		if err != nil {
			return textElem{}, err
		}
		elems, err := p.elemexprListRetained()
		if err != nil {
			return textElem{}, err
		}
		return textElem{elemType: rt, elems: elems}, nil
	}
	// **No empty arm, and the `RParen` case that used to sit here was a forged one (#143).** Both of
	// `elem_list`'s arms begin with a mandatory token — `elemkind` is `FUNC` (parser.mly:1136) and
	// `reftype`'s thirteen arms each consume at least one (:376-389) — so `elem_list` derives nothing
	// empty, and the spec agrees: `elemlist ::= rt:reftype e*:list(elemexpr)`, reftype mandatory
	// (§6.6.9). An empty case *is* reachable in the grammar, but one level up and in exactly one arm:
	// arm 5 is `offset elemidx_list` and `elemidx_list` is `idx_list`, which does have an empty arm
	// (:500). That is where the case now lives, in `elemField`, so `(elem (i32.const 0))` stays legal
	// (29 corpus rows) while `(elem)`, `(elem $x)`, `(elem declare)` and `(elem (table 0) (i32.const 0))`
	// are rejected again — no reference arm reaches an empty list without an offset in front of it, and
	// none reaches one after a `tableuse` at all.
	return textElem{}, p.expectedInstr()
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
		// `select`'s opcode depends on whether a result type was written (0x1b or 0x1c), which is
		// `ambiguousOpcodes`' whole content — so it is not a lookup, and `opBytes` refuses the whole
		// mnemonic rather than guess. It is decidable here all the same, and that is the point of the
		// arm: the choice is **syntactic** (was `(result …)` written?), not a question about the
		// operands' types, so this stratum has the fact `opBytes` lacks. `ref.test`/`ref.cast`, the
		// other two ambiguous mnemonics, genuinely need validation's knowledge and stay refused.
		//
		// `beforeTail`, and the tail is everything after the select rather than its operands
		// (grave #145): `(select … ) :: es` at :678-680 conses onto the front of the `instr_list`
		// this arm's chain bottoms out in.
		return true, p.selectResults(beforeTail, p.instrList)
	case kwCallIndirect, kwReturnCallIndirect:
		// `beforeTail` for the same reason the select arm above takes it: all four arms of
		// `callinstr_instr_list` (:689-701) end in `… :: es`, where `es` is the rest of the
		// enclosing sequence.
		return true, p.callIndirectInstr(beforeTail, p.instrList)
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

// elemexprRetained parses one `elemexpr` into its own sink, so the element's instructions are kept
// apart from its neighbours'.
//
// **A sink per element, not one sink for the list**, and the format is why: `vec const cs` writes a
// count and then one terminated expression per element (encode.ml:1078), so a shared sink would
// concatenate two elements' instructions into a single expression and emit a vector of one. The
// boundary between elements is information the wire carries and the text does not spell, which is
// exactly the kind of fact `intoSink` exists to preserve — the block family uses it for the same
// reason one layer down.
func (p *parser) elemexprRetained() (bool, instrSink, error) {
	var read bool
	s, err := p.intoSink(func() error {
		var e error
		read, e = p.elemexpr()
		return e
	})
	return read, s, err
}

// elemexprListRetained parses `elemexpr_list` (parser.mly:1143-1145) — zero or more elemexprs —
// returning each element's retained expression.
//
// **There was a recognizer twin, `elemexprList`, returning bare `error`; once the table sugar retained
// both its halves nothing called it and `unused` said so.** That is the second time this exact pair
// has collapsed in this file — `elemList` went the same way when every `elem` arm started retaining —
// and the reason is the same both times: the "recognizer wants the reads and none of the values"
// argument holds only while some caller is a recognizer, and section 9 left none. The value-returning
// reader serves both modes on its own, because `intoSink` returns an empty sink when the parse's mode
// says not to retain, so a `ReadModule` parse gets exactly the reads.
func (p *parser) elemexprListRetained() ([]instrSink, error) {
	var elems []instrSink
	for {
		read, s, err := p.elemexprRetained()
		if err != nil {
			return nil, err
		}
		if !read {
			break
		}
		elems = append(elems, s)
	}
	if !p.c.at(RParen) {
		// A `(` this stratum could not read as an elemexpr: #64's, and reported as the boundary
		// rather than as a syntax error.
		return nil, p.expectedInstr()
	}
	return elems, nil
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
	// **Refused before the body is read, not after.** `try_table`'s opcode carries a `vec catch`
	// (encode.ml:257) this file has no encoding for, and its body's instructions *are* encodable — so
	// emitting the body while dropping the opener would produce a function whose control flow is gone
	// and which decodes clean. Refusing here means the body is still parsed (the error propagates, so
	// nothing downstream runs) and no half-encoded function can exist. See refuseUnencodable.
	//
	// `block`, `loop` and `if` are past this frontier and are emitted below; the fourth arm stays
	// behind it, and the refusal is narrowed to it rather than removed.
	if t.Keyword == kwTryTable {
		if err := p.refuseUnencodable(t, "the "+t.Text+" instruction"); err != nil {
			return true, err
		}
	}
	kw := p.c.next()

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
	var (
		arms blockArms
		ft   funcType
		slot *blockTypeSlot
	)
	if t.Keyword == kwTryTable {
		// **Its own name, not `err`**, which is the reading both linters accept: the outer `err` is
		// live from labelingOpt above and reassigned by the else-branch, so `err :=` here is a
		// shadow (`govet`) and `err =` is a sloppy reassignment (`gocritic`). The two findings
		// point at each other, and the way out is that these really are different errors — the same
		// resolution memAccess reached for `resolveErr`. Nothing is currently wrong with any
		// spelling, since this arm returns immediately; two variables spelled `err` in one function
		// is how a later edit comes to test the wrong one.
		if bodyErr := p.handlerBlock(); bodyErr != nil {
			return true, bodyErr
		}
	} else {
		// The body is collected rather than emitted, because the opener precedes it in the image and
		// its blocktype is not known until the signature is recorded — which happens *after* the body
		// is read (see orderedTypeUse). blockTail is the collection; emitBlock below is the ordering.
		ft, slot, err = p.blockSignatureSlot(p.blockTail(&arms, p.instrList))
		if err != nil {
			return true, err
		}
	}

	// `if … else …` is a fifth arm rather than an option on the third (:732-:735), and it carries
	// its *own* `labeling_end_opt` — so `if $a else $a end $a` has two end-labels to check, and
	// the reference checks them against the same opener by concatenating them (`$5 @ $8`).
	if t.Keyword == kwIf && p.c.atKeyword(kwElse) {
		p.c.next()
		// `endErr`, for the try_table arm's reason: a distinct error gets a distinct name rather
		// than shadowing or reassigning the one blockSignatureSlot wrote.
		if endErr := p.labelingEndOpt(label); endErr != nil {
			return true, endErr
		}
		arms.els, err = p.intoSink(p.instrList)
		if err != nil {
			return true, err
		}
	}
	if !p.c.atKeyword(kwEnd) {
		// Not the boundary: `END` is a required token of every arm, so its absence is this
		// stratum's own syntax error. `(func block)` is malformed on the merits.
		return true, p.unexpected()
	}
	p.c.next()
	if err := p.labelingEndOpt(label); err != nil {
		return true, err
	}
	if t.Keyword == kwTryTable {
		// Refused above, so retention never reaches here for this arm — and it emits nothing, since
		// nothing was collected. Stated as a return rather than left to `slot == nil`, because a nil
		// slot reaching blockTypeBytes would be a panic where this is a frontier.
		return true, nil
	}
	// The `end` token is consumed and its label checked before anything is emitted: emission is the
	// last act of a *successful* parse, so a malformed tail cannot leave a partial construct in the
	// sink. The END *byte* is emitBlock's regardless of whether the text spelled the keyword — the
	// folded form does not.
	return true, p.emitBlock(kw, slot, ft, &arms)
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

// blockSignature reads the part `block` and `handler_block` have in common: `typeuse?` then the
// `(param …)*` and `(result …)*` lists, and **then the body, through the tail it is handed**.
//
// The three are ordered and each is optional, which `block_param_body` (:754) and
// `block_result_body` (:760) express as right-recursive lists — `(param)` may repeat, then
// `(result)` may repeat, then `instr_list`. A `(param)` *after* a `(result)` is not in the
// grammar, so it falls out of the loops and is reported by instrList's fallthrough. (Those two
// sentences described a `p.block()` wrapper that stood here as the `block` production's own face
// and was deleted when blockinstr took the slot-returning form for #7's opener emission; the facts
// are the production's, not the wrapper's, so they moved rather than went with it.)
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
	_, _, err := p.blockSignatureSlot(tail)
	return err
}

// blockSignatureSlot is blockSignature for the callers that must *encode* the blocktype: the two
// productions that emit a block opener, where blockSignature's error-only face is what the ones that
// only recognize keep using.
//
// The pairing is typetable.go's — `declareImplicit`/`internImplicit`,
// `inlineFuncTypeExplicit`/`checkExplicit`, `declareBlockImplicit`/`internBlockImplicit` — copied
// rather than re-derived (*lessons are indexed by shape*). Two returns because the encoder needs both
// halves and they are not the same fact: the slot carries what stage 2 resolved, and the signature
// carries the single result an inline blocktype spells as a bare valtype byte, which is never
// interned and so has no slot to be read from.
func (p *parser) blockSignatureSlot(tail func() error) (funcType, *blockTypeSlot, error) {
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
//
// **It returns the signature and the slot because the blocktype encoder needs both**, and it used to
// be two functions — an error-only `orderedTypeUse` wrapping this one. The wrapper's last caller left
// when the code section began writing blocktype immediates, at which point it was dead code whose
// *doc comment* was cited from nine places. Deleting it would have left nine dangling identifier
// citations, so the name moved here instead of the prose moving there: the reader that every one of
// those comments describes is this one. See blockSignatureSlot for the pairing.
func (p *parser) orderedTypeUse(kind signatureKind, tail func() error) (funcType, *blockTypeSlot, error) {
	use, haveUse := idxRef{}, false
	if p.atTypeuse() {
		var err error
		use, err = p.typeuse()
		if err != nil {
			return funcType{}, nil, err
		}
		haveUse = true
	}
	var ft funcType
	// Two loops in this order, never one loop accepting either: the chains are right-recursive and
	// a `(param)` may not follow a `(result)`. One loop taking both would admit a form the
	// reference rejects, which is the accept-direction defect these vectors exist to catch.
	for p.c.at(LParen) && p.c.peek2Keyword(kwParam) {
		if err := p.lpar(kwParam); err != nil {
			return funcType{}, nil, err
		}
		vs, err := p.valtypeList()
		if err != nil {
			return funcType{}, nil, err
		}
		ft.params = append(ft.params, vs...)
		if err := p.rpar(); err != nil {
			return funcType{}, nil, err
		}
	}
	rs, err := p.functypeResult()
	if err != nil {
		return funcType{}, nil, err
	}
	ft.results = rs
	if err := tail(); err != nil {
		return funcType{}, nil, err
	}
	return ft, p.deferBlockSignature(kind, use, haveUse, ft), nil
}

// deferBlockSignature is deferSignature for the shared chain, differing only in the sugar arm's
// conditional interning. See signatureKind, and declareBlockImplicit for why the condition exists.
//
// **It returns the slot rather than only recording the check**, for the reason deferSignature's
// header gives at length: the block opener's immediate is the index the helper produced, and the value
// must arrive through a slot the deferred op fills rather than from a second call. `inline_functype`
// *appends*, so calling it again for the encoder would intern a duplicate type and the defect would be
// invisible here — surfacing as a type-section count, which is exactly the hazard deferSignature was
// written against. Same slot-plus-thunk shape, third instance.
//
// The slot is returned unconditionally, including for the two arms whose signature is not a block's.
// A `callchain` site has no blocktype to encode and discards it; refusing to hand one over would mean
// this function knowing which callers encode, which is the caller's business and already carried by
// `kind`.
func (p *parser) deferBlockSignature(kind signatureKind, use idxRef, haveUse bool, ft funcType) *blockTypeSlot {
	slot := &blockTypeSlot{}
	switch {
	case haveUse:
		p.ctx.deferOp(func() error {
			idx, err := p.ctx.checkExplicit(use, ft)
			if err != nil {
				return err
			}
			// A written typeuse always names an index, so `interned` is true whatever the inline
			// signature was: `VarBlockType x` is the arm (parser.mly:743), with no `([], [t])` case in
			// front of it. `(block (type $t) (result i32) …)` encodes the index, not the valtype byte.
			*slot = blockTypeSlot{idx: idx, interned: true, filled: true}
			return nil
		})
	case kind == blockchain:
		p.ctx.deferOp(func() error {
			idx, interned, err := p.ctx.internBlockImplicit(ft)
			if err != nil {
				return err
			}
			*slot = blockTypeSlot{idx: idx, interned: interned, filled: true}
			return nil
		})
	default:
		p.ctx.deferOp(func() error {
			idx, err := p.ctx.internImplicit(ft)
			if err != nil {
				return err
			}
			*slot = blockTypeSlot{idx: idx, interned: true, filled: true}
			return nil
		})
	}
	return slot
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
//
// **The END byte is emitted here even though no `end` token was read**, which is the encoding half of
// that same asymmetry: `end_ ()` closes `Block`/`Loop`/`If` in `encode.ml` regardless of spelling
// (:250-256), so the two forms produce identical bytes and the terminator belongs to the encoder.
// Reading it off the token stream would emit it for the flat form only.
//
// **No leader-splice manoeuvre, and that is a property of `if_` rather than a shortcut.** `expr1`'s
// plain arm has to collect its leader into a nested sink and splice it after the operands, because the
// leader is parsed first and emitted last. Here the opener is emitted last already — `emitBlock` runs
// after the tail — so a folded `if`'s condition operands, which `ifBody` writes into the *enclosing*
// sink, land ahead of the opcode by construction. What needs diverting is the opposite half: the
// arms, which are read before the opener is emitted and must follow it.
func (p *parser) foldedBlock(leader keywordKind) error {
	kw := p.c.next() // the keyword
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
	var (
		arms blockArms
		body func() error
	)
	switch leader {
	case kwIf:
		// `if`'s tail is the one that fills *both* arms, and the one whose reader cannot be wrapped by
		// blockTail: its operand loop belongs to the enclosing sequence while its two `(then …)`/
		// `(else …)` lists belong to the arms. So the split happens inside ifBody, which is handed the
		// arms rather than diverted wholesale.
		body = func() error { return p.ifArms(&arms) }
	case kwTryTable:
		body = p.handlerClauses
	default:
		// `block` and `loop`, whose tail is a plain `instr_list` (:741/:748). Unreachable for
		// anything else — expr1 dispatched here on exactly these four leaders — and a default
		// rather than a fifth case because the caller owns the domain.
		body = p.blockTail(&arms, p.instrList)
	}
	ft, slot, err := p.blockSignatureSlot(body)
	if err != nil {
		return err
	}
	if leader == kwTryTable {
		// Refused by expr1 before it dispatched here, so retention never reaches this line — and
		// nothing was collected to emit. See blockinstr's matching return for why it is a statement
		// rather than a nil-slot check.
		return nil
	}
	return p.emitBlock(kw, slot, ft, &arms)
}

// ifArms parses `if_` (parser.mly:891-898), the folded `if`'s tail: any number of folded operands
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
//
// **The three lists it reads go to three different places, which is why this takes the arms rather
// than being wrapped by blockTail.** The production's own return says so: `[], $3 c', $7 c'` (:893) is
// a triple, and the sugar arms return an *empty* first component while the recursive arm accumulates
// operands into it (`es @ es0`, :892). Those operands are the condition — they execute before the `if`
// opcode and belong to the enclosing sequence — while `(then …)` and `(else …)` are the arms the
// opcode delimits. So the loop writes through to the active sink and only the two lists are diverted.
func (p *parser) ifArms(arms *blockArms) error {
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
	body, err := p.intoSink(p.instrList)
	if err != nil {
		return err
	}
	arms.body = body
	// `thenErr`/`elseErr` rather than a second `err`: the one above is live and these are different
	// errors, which is the same reading blockinstr's `bodyErr` takes for the same pair of findings.
	if thenErr := p.rpar(); thenErr != nil {
		return thenErr
	}
	if !p.c.at(LParen) || !p.c.peek2Keyword(kwElse) {
		return nil // the one-armed sugar arm, :896
	}
	if elseErr := p.lpar(kwElse); elseErr != nil {
		return elseErr
	}
	els, err := p.intoSink(p.instrList)
	if err != nil {
		return err
	}
	arms.els = els
	return p.rpar()
}

// callIndirectInstr parses `call_indirect`/`return_call_indirect` in either spelling and emits it:
// `callinstr_instr_list` (parser.mly:689-701) for the flat form, `expr1`'s four arms (:817-823) for
// the folded one. The leader is unconsumed on entry.
//
// **One reader for eight productions, and the eight collapse to three parameters.** The reference
// spells out `{CALL_INDIRECT, RETURN_CALL_INDIRECT} × {idx written, sugar} × {flat, folded}` because
// menhir decides by lookahead; here the mnemonic picks the opcode, a peek decides whether a table
// index was written, and the placement is the caller's. Writing them separately would put the
// immediate *order* below in eight places, which is the one thing about this instruction that is easy
// to get wrong and impossible for the suite to see.
//
// # The immediates are written in the opposite order from the text
//
// `CallIndirect (x, y) → op 0x11; idx y; idx x` (encode.ml:275) — `x` is the table and `y` the type,
// so the **type index comes first on the wire** while the text writes the table first (and usually
// omits it). `ReturnCallIndirect` at :278 repeats it. Measured rather than read off the arm:
// `wat2wasm --enable-all` on `(call_indirect 1 (type $t2) …)` with type 2 and table 1 emits
// `11 02 01`.
//
// Getting this backwards writes a legal module: both immediates are u32 LEBs, so a swapped pair
// decodes clean and calls through the wrong table with the wrong type — and it is *invisible* for
// every module whose table and type indices are both 0, which is nearly every `call_indirect` in the
// suite. That is why the round-trip rows for this use index 1 for one of them.
//
// # Why both immediates go through one patch
//
// The type index is a **stage-2 fact**: `callchain` interns unconditionally (:709/:847), and an
// explicit `(type $t)` may name a type defined after this function. The table index resolves at the
// cursor. So the pair cannot be built with `appendImm` for one and `patch` for the other —
// `resolveFuncs` lets a patch *replace* the immediates rather than append to them, which
// `retainIdx`'s "cannot yet encode a deferred index after another immediate" refusal is the other
// face of. One closure computes both, in wire order, which is also the only shape that makes the
// order a single statement.
//
// Structurally the signature half is the same ordered chain as `blockSignature` — optional `typeuse`,
// then `(param …)*`, then `(result …)*` — and **shared with it**, through `orderedTypeUse`, with the
// tail passed as a parameter: `callexpr_results` ends in `expr_list` (:860), the folded operands,
// where `block_result_body` ends in `instr_list`.
//
// That was not always so, and the correction is the point. This comment used to say the chains were
// *not* shared, "because they bottom out differently", and cited TestOrderedTypeUseChainsAgree as
// what stops two copies of the param/result ordering from drifting. Sharing then landed — which is
// the *right* outcome, since the design debt's remedy was taken rather than merely tripwired — and
// **neither half of the sentence was swept**: the prose kept describing two copies, and the citation
// kept naming a test that has never existed. So a reader chasing the drift risk found a claim with
// nothing behind it. Both faces of one law: *a ruling retroactively falsifies prose written before
// it*, and a tripwire whose subject dissolves is re-pointed or discharged, never left cited. It is
// discharged here — one function cannot drift from itself — and the ordering it enforces is pinned
// by TestFoldedAndFlatSignaturesAgree. Swept with #88 by comparing every `Test*` cited in the tree
// against every `Test*` defined.
//
// The 30 field-ordering vectors split across both readers — block.wast/loop.wast/if.wast reach
// `blockSignature`, call_indirect.wast/return_call_indirect.wast reach this one — so a drift between
// them is a drift the board would show only in one of two files.
// **Its sugar arm interns unconditionally** (:847: `inline_functype c ft $loc($1)`), with no
// `([], [t])` case — so `(call_indirect (result i32) …)` creates a type where `(block (result i32))`
// does not. Hence callchain rather than blockchain, and see signatureKind for why that is a
// parameter and not read off the tail.
func (p *parser) callIndirectInstr(place instrPlacement, tail func() error) error {
	kw := p.c.next() // CALL_INDIRECT or RETURN_CALL_INDIRECT
	// The table index is a **sugar arm** (:693/:699 flat, :819/:823 folded), not an `idx_opt`: a NAT
	// or VAR here is the table, and anything else starts the type chain. Defaulted to `0l` by the
	// sugar arms, and defaulted the same way here — the wire form has no optional field, so the
	// absent index is a written zero rather than an omission.
	tab := idxRef{}
	if p.c.at(NatTok) || p.c.at(VarTok) {
		var err error
		tab, err = p.idxValue()
		if err != nil {
			return err
		}
	}
	// The tail runs *inside* the chain — `callinstr_results_instr_list` bottoms out in it (:722) —
	// which is what makes the signature intern after the body, per orderedTypeUse's evaluation-order
	// paragraph. So the chain is read with the tail, and the instruction is placed around it below.
	//
	// A nested sink is what reconciles that with `beforeTail`: the tail has already been emitted by
	// the time the slot exists, and a flat `call_indirect` must precede it. `placeInstr` cannot help
	// here — it *calls* the tail — so the diversion happens at this level and the collected tail is
	// spliced on the side the placement names.
	var (
		slot    *blockTypeSlot
		nested  instrSink
		chainer = func() error {
			var err error
			nested, err = p.intoSink(tail)
			return err
		}
	)
	_, slot, err := p.orderedTypeUse(callchain, chainer)
	if err != nil {
		return err
	}
	if !p.retaining() {
		return nil
	}
	op, ok := opBytes(kw.Text)
	if !ok {
		// Both mnemonics are unambiguous rows, so this cannot fire on today's table; written for
		// `opBytes`' other callers' reason — "cannot fail" is a claim about a generated file.
		return errf(kw, "cannot yet encode the %s instruction (#8)", kw.Text)
	}
	in := instr{op: op, patch: p.callIndirectImm(slot, tab, kw)}
	if place == beforeTail {
		p.emit(in)
		p.emitSink(&nested)
		return nil
	}
	p.emitSink(&nested)
	p.emit(in)
	return nil
}

// callIndirectImm is the immediate thunk: the type index then the table index, which is the wire
// order and the reverse of the written one (encode.ml:275/:278).
//
// Both indices are resolved here rather than one at the cursor, for `callIndirectInstr`'s stated
// reason — a patch replaces the immediates, so a pair cannot be split across the two mechanisms. The
// table's resolution is *no later* for it: `catTable` is a space whose definitions precede the code
// section in the image, so resolving it in stage 2 gives the same answer the cursor would, and grave
// #130's both-orders question is unaffected.
func (p *parser) callIndirectImm(slot *blockTypeSlot, tab idxRef, kw Token) func() ([]byte, error) {
	return func() ([]byte, error) {
		if !slot.filled {
			return nil, errf(kw, "internal: %s's type read before stage 2 resolved it", kw.Text)
		}
		if !slot.interned {
			// `callchain` interns unconditionally, so this is unreachable — and it is an error
			// rather than a fallback because the alternative is `blockTypeEmptyByte`, a *legal*
			// blocktype byte that would encode `call_indirect` with type index 0x40. Declared and
			// tracked (#6's ruling) at the one site that could produce it.
			return nil, errf(kw, "internal: %s's signature interned no type", kw.Text)
		}
		var w writer
		w.u32(slot.idx)
		idx := tab.idx
		if tab.isVar {
			var err error
			idx, err = p.ctx.tables.resolveSpaceIdx(tab)
			if err != nil {
				return nil, err
			}
		}
		w.u32(idx)
		return w.b, nil
	}
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
//
// **Where the instruction goes relative to its tail is the caller's, and the two arms disagree** —
// grave #145. The productions are `select (…) :: es` (:678-680) and `es, select (…)` (:815-816), and
// those are opposite: the flat arm's `es` is the `instr_list` that *follows* it in the source, so the
// select comes first, while the folded arm's `es` is its operand `expr_list`, so the select comes
// last. This function emitted after the tail for both, which is right for the folded arm and puts a
// flat select behind everything that follows it. See instrPlacement.
//
// The *whether* flag is now retained (see selectOpByte), which this function was previously the
// place that threw away: `_, err := p.functypeResult()` discarded the slice, and the flag was never
// computed at all because a recognizer does not need it.
func (p *parser) selectResults(place instrPlacement, tail func() error) error {
	kw := p.c.next() // SELECT
	// The flag is read from the cursor rather than from the slice, for selectOpByte's reason: a
	// written `(result)` with no valtypes yields an empty slice and must still encode as `0x1c`.
	// `functypeResult`'s loop condition is this same peek, which is what makes the two agree.
	wrote := p.c.at(LParen) && p.c.peek2Keyword(kwResult)
	results, err := p.functypeResult()
	if err != nil {
		return err
	}
	// The vector's valtypes may name a forward-referenced type, so they resolve in stage 2 through
	// `patch` — the same slot the `call $f` arm uses, and the reason `instr` has the field at all.
	// The bare form has no immediates and needs no patch, so it does not take one: a patch that
	// returned an empty slice would work and would put a thunk where a constant belongs.
	in := instr{op: selectOpByte(wrote)}
	if wrote {
		in.patch = func() ([]byte, error) { return p.selectResultBytes(results, kw) }
	}
	return p.placeInstr(place, in, tail)
}

// instrPlacement says whether an instruction is emitted before or after the tail production it
// carries — the fact grave #145 is made of.
//
// **A parameter because the reference's two productions disagree, and the disagreement is invisible
// in the common case.** `select`, `call_indirect` and `return_call_indirect` each have a flat form
// whose tail is the *rest of the enclosing sequence* and a folded form whose tail is its own
// operands, and the semantic actions cons the instruction onto opposite ends: `(select …) :: es`
// against `es, select (…)`. So the two forms of one instruction have opposite emission orders, and
// the reason a single emission point survived review is that a flat `select` written last — which is
// every flat `select` in the round-trip table and nearly every one in the suite — has an empty tail,
// where both orders coincide.
//
// Not inferred from the tail function, though the correlation holds today (`instrList` is flat,
// `exprList` is folded): that is `signatureKind`'s reasoning at the sibling site, and the same
// argument applies with more force here, since `instrList` is also the tail of the *folded* block
// family. Two facts that happen to agree are two parameters.
type instrPlacement int

const (
	beforeTail instrPlacement = iota // flat: the tail is what follows the instruction in the source
	afterTail                        // folded: the tail is the instruction's operands
)

// placeInstr runs a tail and emits an instruction on the side of it the placement names.
//
// The `beforeTail` half needs no nested sink — it emits and then lets the tail append after, which is
// the ordinary direction a writer moves. It is `afterTail` that would need one if anything had been
// emitted before the tail ran, and nothing has: the instruction is built from tokens read before it,
// not written. That asymmetry is why this is four lines rather than an `intoSink` call.
func (p *parser) placeInstr(place instrPlacement, in instr, tail func() error) error {
	if place == beforeTail {
		p.emit(in)
		return tail()
	}
	if err := tail(); err != nil {
		return err
	}
	p.emit(in)
	return nil
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
		// The folded spelling of the same refusal `blockinstr` makes, at the other production, and
		// narrowed to the same one arm: `try_table`'s `vec catch` has no encoding here. Two sites
		// because the reference has two productions, not because the reason differs.
		if leader == kwTryTable {
			if err := p.refuseUnencodable(p.c.peek(), "the "+p.c.peek().Text+" instruction"); err != nil {
				return err
			}
		}
		return p.foldedBlock(leader)
	case kwSelect:
		// The folded spelling of the same instruction, differing only in its tail — `expr_list`
		// against `instr_list` (:837-840 against :682-686). See flatSelectOrCall's arm for why the
		// opcode choice is decidable at this stratum where `opBytes` refuses it.
		//
		// `afterTail`, the opposite of that arm's, because here `es` is the operand list and the
		// select follows it — `es, select (…)` (:815-816). See instrPlacement (grave #145).
		return p.selectResults(afterTail, p.exprList)
	case kwCallIndirect, kwReturnCallIndirect:
		// `afterTail`, the folded arms' order: `es, call_indirect (…) x` (:817-823), where `es` is
		// the operand list. Otherwise identical to the flat arm — see callIndirectInstr.
		return p.callIndirectInstr(afterTail, p.exprList)
	default:
		// Falls out of the switch to the first arm below. Written as an empty default rather than
		// left implicit because `exhaustive` reads a bare fall-through as an unhandled enum, and
		// the honest statement is that this switch dispatches nine arms out of 173 keyword kinds
		// and hands everything else to `plaininstr` — which is a fallback, not an omission.
	}
	// `plaininstr expr_list` (:814), the arm #63 owned — and **the one place emission order is not
	// parse order**. The leader is parsed first and denotes the *last* instruction of the sequence:
	// `(i32.add (i32.const 1) (i32.const 2))` is `i32.const 1 · i32.const 2 · i32.add`. So the leader
	// goes into a sink of its own, the operands accumulate into the active one, and the leader is
	// spliced afterwards.
	//
	// Getting this backwards emits a module that decodes clean, validates, and computes a different
	// answer — invisible to every vector, because the suite's folded modules are ones it expects to
	// work. Pinned by TestEncodeRoundTripsThroughTheDecoder, whose two-deep folded row and its flat
	// twin state the body's instruction order in the want column; reversing this splice fails the
	// folded row with `i32.add` leading, and the flat row is what stops the fix being "reverse both".
	//
	// **`retaining()` and not `p.retain` here, which is the opposite of `intoSink`'s fix (grave #144)
	// and is correct for the reason that grave names.** This swap *consumes* an installed sink rather
	// than establishing one: the last line is `p.sink.splice(&leaderSink)`, which needs a non-nil outer
	// sink to splice into, and `expr1` reaches this arm only from inside a function body where
	// `funcField` installed one. So the question really is "is a sink installed", and asking the mode
	// instead would nil-dereference on any future module-field-scope caller — the same conflation
	// failing in the other direction. Stated because the sweep after #144 had to decide this site on
	// its merits, and a reader repeating that sweep should not have to re-derive the answer.
	if !p.retaining() {
		if _, err := p.plaininstr(); err != nil {
			return err
		}
		return p.exprList()
	}
	var leaderSink instrSink
	outer := p.sink
	p.sink = &leaderSink
	_, err := p.plaininstr()
	p.sink = outer
	if err != nil {
		return err
	}
	if err := p.exprList(); err != nil {
		return err
	}
	p.sink.splice(&leaderSink)
	return nil
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
