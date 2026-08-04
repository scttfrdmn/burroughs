package text

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The text→binary bridge's module layer: turning what the parse retained into a binary image
// (#8, 0011 part 2).
//
// # What this reads, and why that is the whole design
//
// The parse already retains the type index space. `runDeferred` resolves it into `c.typeCtx`,
// a `[]resolvedComp` in index order, because `inline_functype_explicit` needs to *compare*
// signatures structurally — so the content exists for a grammar reason that predates this file.
// The emitter reads that, and 0011's rule is untouched: nothing leaves the package as a module
// value, `ReadModule` stays error-only, and the only thing crossing the boundary is **bytes**.
//
// That is 0006's load-bearing-spot rule applied to sequencing. The alternative — a retention pass
// shaped by what an encoder wants — designs the parser's memory from its only consumer's
// requirements. Here the requirement runs the other way: the emitter takes what the grammar already
// had to keep, and each further section is a question about what the *grammar* needs.
//
// # Growing one section at a time, and stating the frontier
//
// Sections arrive **one at a time, each a vertical slice rather than a stub** — a section is emitted
// only when the parse side retains its content in full and the decode side represents it in full, so
// every one round-trips end to end and can be checked against the cross-check corpus. A wider surface
// would need retention that does not exist yet, and building that retention *and* its consumer in one
// motion shapes a representation in the load-bearing spot.
//
// Landed so far: **type (1), function (3), table (4), memory (5), export (7), code (10)**, plus
// import (2), written in id order. This paragraph said "the type section and nothing else" for two
// PRs after that stopped being true, which is the drifted-citation defect wearing a scope note — so
// it now names the set rather than the moment, and `secType`'s const block below is the enumeration
// a reader can check it against.
//
// **The code section is the first one that is not a vertical slice of a field, and the frontier
// moved inside an instruction as a result.** Sections 1–7 are each a *field* the parse either
// retains in full or refuses; a function body is a sequence whose members are individually
// encodable or not, so `refuseUnencodable` refuses per *instruction* and the module withdraws. See
// code.go's header for the shape of that frontier and for the mechanism census that priced it.
//
// **The export section is the first one with no payload branch, and that is a fact about exports
// rather than a simplification.** An `externtype` carries the thing's type, so section 2's five kinds
// each write their own descriptor; an `externidx` carries only an index into a space that already
// holds the thing, so all five kinds collapse to a kind byte and a `u32` (encode.ml:1009-1014). The
// kind bytes are `externtype`'s own, reused rather than re-listed, and that reuse's premise —
// identical order in both grammars — is machine-checked by
// TestExternKindByteAgreesForBothSections rather than assumed.
//
// So `EncodeModule` **refuses** every module it cannot fully encode, naming what stopped it.
// Emitting a module while silently dropping its function section would produce bytes that decode
// clean and mean something else — an accept-direction defect, which is the direction no suite vector
// covers (contract §9 G-3) and the exact failure this bridge exists to be checked for.
//
// The refusal is a *frontier*, not a malformedness, and the error says so: it names the construct
// and this issue, never a spec string. Reporting "malformed" for a module the spec calls well-formed
// would lie about the input to conceal a gap in the engine, which is the ruling on #5 pointed at an
// unfinished encoder instead of at a gate.
//
// # What an independent witness says so far
//
// Measured against #67's wabt corpus, joined on (file, ordinal): of the suite's 2150
// parser-accepted text modules this encodes **926** in full, **878** of which the corpus can be
// joined to — and those 878 agree **byte for byte**, 0 disagreements. The 48 unjoined fall in exactly
// nine files, `annotations`, `memory`, `memory64`, `table`, `table64`, `table_init`, `table_init64`,
// `tag` and `type-subtyping`, and every one of the nine is in the manifest's `skipped_files` with
// wabt's own reason, so the gap is accounted for rather than merely stated. The three files the code
// section *added* to that list were already excluded wholesale by the generator — `table_init` on
// `arrayref`, `type-subtyping` on `sub` — so learning to write a section can only ever enlarge the
// unjoined count by finding more modules inside files the corpus does not carry, never by failing to
// join something it does.
//
// The figure carries its vacuity check, because *exactly zero* on an agreement is the tell grave #106
// was filed for, and at the type section's landing it was the right question to press: 9 of 10
// agreements were 8-byte bare preambles and the whole witness was one module. It is now nowhere near
// vacuous, and the size distribution is the part that changed rather than only the count — **869 of
// the 878 are longer than a bare preamble**, led by `names#2` at **7584 bytes**, `float_literals#0` at
// 3010, and four SIMD arithmetic modules between 842 and 1944. Before this section the largest
// agreement in the corpus was `type#0` at 148 bytes; a seven-kilobyte image agreeing byte for byte
// with a toolchain that has never seen this parser is a different order of claim, because a code
// section is where an off-by-one immediate lives and every one of those bytes had to be right.
//
// **Both the before and after figures above were measured with the same probe, deliberately** — a
// delta between two instruments is not a delta. The 196/156/156/0/147 baseline is a re-run against
// the merge-base, and the re-run is also where the *denominators* got checked for the first time:
// 2150 is `module text` (2143) plus `(module quote …)` (7), and the 1229 `assert_malformed` inner
// quote modules contribute **0 accepted**, so they were never inflating it. The probe's first
// revision this cycle counted only `KindModuleText` and reported 2143/919 — three figures reproducing
// the comment exactly while two did not, which is the tell that the *narrowing* was the change. Same
// enumeration defect the paragraph below records fixing for the ordinal, never re-derived for the
// denominator: a quote module is a module.
//
// **And the zero was earned twice, which is the part worth recording.** The first re-measurement
// reported 99 agreements and **one disagreement**, `token#11` — ours 34 bytes, wabt 25 — and the
// diff was the tell rather than the count: wabt's image had a *function and code* section for a
// module whose text is one import and no functions, so it was not the same module. The probe's
// ordinal counted every command carrying a `Source`, which includes `assert_malformed`'s inner quote
// modules, and the key had shifted. Two probe defects in a row, in fact: before that it reported
// **0 joined of 141** with `type#0` among the misses, which is *exactly zero on a join that used to
// work* — the same tell in its cleanest form, and the cause was `File` being the base name without
// its `.wast`. Neither figure was a finding about the encoder. An agreement measured through a
// mis-keyed join is not evidence, in either direction.
//
// Byte equality is *evidence*, deliberately not the criterion: the corpus is an authority on which
// module the text denotes, not on encoding style, which is why #67's comparator compares `[]Instr`.
// A future divergence in a legal-but-different encoding is a fact about wabt's style and must not be
// read as a defect here.

// The section ids this emitter writes. The remaining ten arrive with their sections — naming them
// now would be constants nothing reads, which is the placeholder shape #6 rules on, and the honest
// version of "later" here is an empty line rather than a tracked stub.
const (
	secType     byte = 1
	secImport   byte = 2
	secFunction byte = 3
	secTable    byte = 4
	secMemory   byte = 5
	secExport   byte = 7
	secCode     byte = 10
)

// tagFunc is `comptype`'s functype form, 0x60 — which is -0x20 read as the sleb(7) the decoder
// reads it as (decodeCompType's comment has the vector that forces the signedness). Written as the
// byte because that is what a minimal sleb(7) of -0x20 is.
const tagFunc byte = 0x60

// EncodeModule parses src and emits the binary image of the module it denotes.
//
// The return is bytes and an error, never a module value — 0011's surface rule, which this does not
// widen: `binary.DecodeModule` stays the codebase's only route to a module, so these bytes are
// checked by being decoded rather than by being inspected.
//
// **This is not `ReadModule` with an output parameter.** A caller wanting a well-formedness verdict
// keeps using `ReadModule`, because this additionally refuses well-formed modules whose sections it
// cannot yet write. Routing a verdict through here would convert the encoder's frontier into a
// judgement about the module.
func EncodeModule(src []byte) ([]byte, error) {
	p, err := parseModule(src, build)
	if err != nil {
		return nil, err
	}
	return p.encode()
}

// parseModule runs the whole front end and hands back the parser, context and all.
//
// Factored out of `ReadModule` rather than duplicated into `EncodeModule`, because the entry
// sequence is three steps with a trailing-token check that is easy to omit — and two places knowing
// one sequence is the drift risk 0006 names. `ReadModule` is now this plus discarding the parser,
// which is exactly what its error-only surface means.
// parseMode is whether a parse builds a module or only judges one.
//
// A named type rather than a bare bool, because the two calls sit in different files and
// `parseModule(src, true)` at the `EncodeModule` site would be a coin flip to a reader — while the
// wrong value is not a compile error but an *accept-direction* one, the whole class this package
// spends its comments on. The zero value is `recognize`, which is the conservative half: a parse that
// forgets to state its mode refuses nothing.
type parseMode bool

const (
	// recognize answers well-formedness and retains nothing. `ReadModule`'s mode, and the 4162
	// vectors' mode: the instruction frontier must be invisible here.
	recognize parseMode = false
	// build retains what the encoder writes, and therefore *refuses* well-formed modules whose
	// instructions have no encoding yet. `EncodeModule`'s mode.
	build parseMode = true
)

func parseModule(src []byte, mode parseMode) (*parser, error) {
	c, err := newCursor(src)
	if err != nil {
		return nil, err // a lex error, unwrapped; see newCursor
	}
	p := &parser{c: c, ctx: newContext(), retain: bool(mode)}
	if err := p.module(); err != nil {
		return nil, err
	}
	if !p.c.at(EOF) {
		return nil, p.unexpected()
	}
	return p, nil
}

// encode writes the module the parse retained.
//
// **runDeferred is not called here.** `moduleFields` already ran it: the parse is complete when this
// is called, and the resolved table is a *result*, not something to recompute.
//
// The first draft of this function called it again, and the reason that is wrong is not the reason
// the draft's comment gave — which is worth recording, because the wrong reason was more alarming
// and would have been believed. The comment said a second run discards the implicit types
// `inlineFuncType` interned, since `runDeferred` rebuilds `typeCtx` from `typeDefs` alone. Measured
// instead of reasoned: **`typeCtx` comes back identical**, because every deferred thunk re-interns
// the same signatures in the same order. What does *not* come back identical is `types.count`, which
// `inlineFuncType` increments per interned type and which therefore double-counts — 1→2 on
// `(module (func))`, 2→4 on a module with two distinct inline signatures.
//
// So the real hazards are two, and neither is the one first written down:
//
//   - `types.count` is corrupted. Nothing reads it after the parse today (name binding is over, and
//     `funcTypeAt` bounds on `len(typeCtx)`), so this is latent rather than live — which is exactly
//     the kind of thing that becomes live silently.
//   - **The idempotence is contingent, not designed.** The thunks were written to run once; that
//     they survive a second run is an accident of what they currently do. A future thunk that
//     appends unconditionally would make a second run change the module, and nothing would say so.
//
// Hence the rule is structural rather than a warning about a specific corruption: the phase has one
// owner, `moduleFields`, and this function reads. TestEncodeDoesNotRerunTheDeferredPhase asserts it
// by watching the *whole* type-space state across `encode`, count included — the count is the half
// that falsifies, and the first version of that control checked only the table and so was stillborn.
//
// Section order is the binary format's and is not negotiable: `checkSectionOrder` rejects an image
// whose ids do not ascend, so it lives here, once, in the sequence of calls.
func (p *parser) encode() ([]byte, error) {
	if err := p.encodableOrErr(); err != nil {
		return nil, err
	}
	// **Before the preamble, not between sections.** Sections 3 and 10 are the first whose content
	// needs resolving rather than merely reading, and resolving them here keeps the invariant every
	// other section relies on: once bytes start being written, nothing can fail. See resolveFuncs.
	funcs, err := p.resolveFuncs()
	if err != nil {
		return nil, err
	}

	var w writer
	w.bytes(binary.Magic[:])
	w.u32le(binary.Version)
	// An empty type section is omitted rather than written with a zero count. Both decode to the
	// same module, and every producer omits it — including wabt, which the cross-check corpus
	// compares against and which there is no reason to gratuitously differ from. `writer.section`
	// deliberately leaves this to the caller (its comment says why); this is the caller deciding.
	if len(p.ctx.typeCtx) > 0 {
		w.section(secType, p.encodeTypes)
	}
	if len(p.ctx.imports) > 0 {
		w.section(secImport, p.encodeImports)
	}
	// Sections 3 and 10 are written from one list and guarded by one condition, because the format
	// requires them to agree: a function section declaring N functions and a code section carrying
	// M bodies is malformed on the merits (`function and code section have inconsistent lengths`), and
	// two conditions here would be two places that could disagree about N.
	if len(funcs) > 0 {
		writeFuncSection(&w, funcs)
	}
	if len(p.ctx.tabDefs) > 0 {
		w.section(secTable, p.encodeTables)
	}
	if len(p.ctx.memDefs) > 0 {
		w.section(secMemory, p.encodeMemories)
	}
	if len(p.ctx.exports) > 0 {
		w.section(secExport, p.encodeExports)
	}
	if len(funcs) > 0 {
		writeCodeSection(&w, funcs)
	}
	return w.b, nil
}

// encodableOrErr reports the first construct this emitter cannot write, with its position.
//
// **Checked against the parse's own retention, never against the source text.** Scanning src for
// `(func` would be a claim about text where the question is about content, and it would miss every
// sugar spelling — which is precisely where an emitter silently drops a field.
//
// Two questions, and they are genuinely different:
//
//   - **A field kind with no emitter.** `firstNonType` is recorded by `moduleField` as the parse
//     dispatches, so the check sees exactly what the grammar saw. Not inferred from the index
//     spaces: `space.count` advances for *imported* entries too, so a count-based test could not
//     tell `(func)` from `(import "" "" (func))`, and — worse — an export binds no space at all, so
//     an export-only module would pass a count-based check and be emitted with the export dropped.
//
//   - **A type the encoder has no byte for.** Every valtype the parser accepts is not yet
//     encodable: `(ref func)` needs GC's 0x64 prefix, and a struct or array comptype has no
//     retained fields to write. Asked through the *same function the emitter writes with*, so the
//     check and the emitter cannot disagree about what is implemented — one concept, one trigger.
func (p *parser) encodableOrErr() error {
	if p.ctx.haveNonType {
		// The token's *text*, never its kind. A kind is the lexer's token class and does not name a
		// spelling — measured: lower-casing a kind reproduces its own keyword for only 96 of 173,
		// and `BINARY` lower-cases to a literal that lexes to `BIN`. Quoting Token.Text is the
		// reference's own practice for the same reason (bindAbs), and it makes the question moot.
		//
		// The message names the *field*, and for memory and table that is now imprecise in one
		// direction only: a refused `(memory …)` is a memory in one of its three unencodable arms,
		// so the reader is told which field to look at and not which arm. Accepted rather than
		// papered over — naming the arm needs the arm threaded into the record, and the field is
		// what a reader edits.
		return errf(p.ctx.firstNonType, "cannot yet encode this (%s …) field: the emitter writes "+
			"the type, import, function, table, memory, export and code sections (#8)",
			p.ctx.firstNonType.Text)
	}
	// **The withdrawal check.** Every *defined* memory and table must have been retained, or a
	// section is short by one and the image means something else. `clearNonTypeField` is a claim an
	// arm makes about itself and nothing at the call site proves it; this is the proof, and it is
	// structural rather than per-arm — a sixth arm added to `memoryField` that withdraws without
	// recording fails here rather than emitting a truncated section.
	//
	// Counted per kind, both directions, because either mismatch is a defect: fewer retained than
	// defined is a dropped entry, and more retained than defined means something recorded content for
	// a field that is not a definition (an import), which would emit it twice.
	//
	// **This check cannot currently be made to fire, and that is stated here rather than left to be
	// discovered.** Four defects were installed against it and all four passed: disabling the
	// comparison outright changes nothing, dropping `defineMemory` while keeping `noteDefined` does not
	// compile (the retained value goes unused, so Go rejects it), and adding a spurious `noteDefined`
	// to the `(data …)` sugar arm is invisible because *the frontier refuses that module first* — the
	// arm does not withdraw, so `encodableOrErr` returns at the `firstNonType` check above and never
	// reaches this loop. The structural reason is general: this loop only runs on modules where every
	// memory and table arm was the retaining one, which is exactly the population where the counts
	// agree by construction.
	//
	// So it is a **tripwire for an arm that does not exist yet**, not a control over today's code, and
	// per the declared-and-tracked ruling that is legitimate only if it is *labelled* as one. The arm
	// it waits for is the one this comment describes: a sixth `memoryField`/`tableField` arm that
	// withdraws the frontier and records nothing, or records for an import. When such an arm is
	// written, this check fires and the falsification budget is spent then. Keeping it costs four
	// lines; deleting it and rediscovering the need is how a section comes to be short by one entry.
	//
	// What is *not* claimed: that removing this loop would be caught by the suite. It would not, today,
	// by any of the six probes run. An unfalsifiable control announced as unfalsifiable is scaffolding
	// with a name tag; the failure mode the graveyard names is the one that stays quiet about it.
	for _, k := range [...]struct {
		kind     importKind
		retained int
	}{
		{importMemory, len(p.ctx.memDefs)},
		{importTable, len(p.ctx.tabDefs)},
		// **The func row can fire, unlike the two above.** `funcField` has one retaining path and one
		// import path, and the import path returns through `inlineImportTail` without touching
		// `p.funcs` — so a future arm that withdraws the frontier and forgets to append is a section 3
		// short by one entry, and section 10 short by a body, which is the *malformed* inconsistent-
		// lengths error rather than a silently different module. Falsified by deleting the
		// `p.funcs = append(…)` line: `(module (func))` then emits an 8-byte bare preamble and this
		// fires with `0 function definitions retained, 1 defined`.
		{importFunc, len(p.funcs)},
	} {
		if got, want := k.retained, int(p.ctx.defCount[k.kind]); got != want {
			return fmt.Errorf("text: internal: %d %s definitions retained, %d defined — an arm "+
				"withdrew the encoder's frontier without recording its content (#8)", got, k.kind, want)
		}
	}
	// **The import withdrawal check, and unlike the loop above it can fire.** The two counts come from
	// different code — `importsSeen` from `noteImport`, which every import spelling passes through in
	// the grammar, and `len(imports)` from `defineImport`, which the emitter reads — so a sixth import
	// spelling added to the grammar without a `defineImport` call fails here rather than emitting a
	// section short by one.
	//
	// That is not hypothetical the way the memory/table version is. Six arms produce an import today
	// (the field plus five sugars), all six were unretained before this PR, and the population this
	// runs on is every module the frontier admits — which now *includes* imports, so an arm that
	// withdraws and forgets is reachable. Falsified by deleting the `defineImport` call from
	// `inlineImportTail`: `(module (memory (import "m" "x") 1))` then emits an 8-byte bare preamble,
	// and this fires. Both directions, because a doubled `defineImport` would emit the import twice.
	if got, want := len(p.ctx.imports), p.ctx.importsSeen; got != want {
		return fmt.Errorf("text: internal: %d imports retained, %d parsed — a spelling withdrew the "+
			"encoder's frontier without recording its content (#8)", got, want)
	}
	// The same check for section 7, and it can fire for the same reason: `exportsSeen` is the
	// grammar's, incremented in `exportHead`, and `len(exports)` is the emitter's, appended by
	// `defineExport`. Two spellings reach `exportHead` and each calls `defineExport` separately, so the
	// counts are produced by different code and a spelling that parses an export without retaining it
	// fails here.
	//
	// Falsified by deleting the `defineExport` call from `inlineExports`:
	// `(module (memory (export "e") 1))` then emits no section 7 at all — a module that decodes clean
	// and exports nothing — and this fires with `0 exports retained, 1 parsed`. That is precisely the
	// defect the sugar shipped with for four PRs, which is the argument for the check: it was invisible
	// then because nothing compared the two numbers.
	if got, want := len(p.ctx.exports), p.ctx.exportsSeen; got != want {
		return fmt.Errorf("text: internal: %d exports retained, %d parsed — a spelling withdrew the "+
			"encoder's frontier without recording its content (#8)", got, want)
	}
	for i, t := range p.ctx.tabDefs {
		if _, ok := valTypeByte(t.elem); !ok {
			return fmt.Errorf("cannot yet encode table %d: element type %s needs a parameterized "+
				"reference encoding, which arrives with the GC gate (#8)", i, t.elem)
		}
	}
	// An import's descriptor can hold a valtype in two of its five forms, and both are the same
	// frontier as a table definition's element type: `(import "m" "t" (table 1 (ref func)))` needs
	// GC's 0x64 prefix. Asked through `valTypeByte`, the one predicate, so this cannot disagree with
	// what `encodeImports` will write.
	//
	// Refused per *import* rather than folded into the loops above, because the position a reader
	// needs is the import's index in section 2 — a table refusal quoting `table 0` when the module has
	// no table section would be an error naming a thing the image does not contain, which is the
	// wrong-layer tell #36 records in message form.
	for i, im := range p.ctx.imports {
		var v resolvedVal
		switch im.desc.kind {
		case importTable:
			v = im.desc.table.elem
		case importGlobal:
			v = im.desc.global.val
		case importFunc, importMemory, importTag:
			continue // no valtype in the descriptor: a type index, a limits, or both
		}
		if _, ok := valTypeByte(v); !ok {
			return fmt.Errorf("cannot yet encode import %d: %s needs a parameterized reference "+
				"encoding, which arrives with the GC gate (#8)", i, v)
		}
	}
	// The type-space refusals carry no offset, and that is deliberate rather than a shortcut. A
	// position is a *malformedness* affordance — it points a user at the text that is wrong — and
	// nothing here is wrong: the module is well-formed and the encoder is unfinished. An implicit
	// type has no source token of its own in any case (it is interned from a signature), so a
	// position would sometimes be honest and sometimes invented, which is worse than absent.
	for i, ct := range p.ctx.typeCtx {
		if !ct.isFunc {
			// A struct or array slot. The parse retains no fields for these — `compType`'s comment
			// says why: every consumer treats a non-func type as `non-function type <n>` and never
			// reads its fields. So there is nothing to write, and writing an *empty* struct would be
			// wrong content in a right-sized slot: a module that decodes clean and means something
			// else. Refused instead.
			return fmt.Errorf("cannot yet encode type %d: a struct or array type's fields are not "+
				"retained (#8)", i)
		}
		for _, group := range [][]resolvedVal{ct.ft.params, ct.ft.results} {
			for _, v := range group {
				if _, ok := valTypeByte(v); !ok {
					return fmt.Errorf("cannot yet encode type %d: %s needs a parameterized "+
						"reference encoding, which arrives with the GC gate (#8)", i, v)
				}
			}
		}
	}
	return nil
}

// encodeTypes writes the type section from the resolved type index space.
//
// One entry per slot, which is what keeps indices aligned — `CompType`'s comment names the
// alternative as a defect visible only in the all-gates-on lane. Here the alignment is free, because
// `encodableOrErr` has already refused every module holding a slot this cannot fill.
func (p *parser) encodeTypes(w *writer) {
	w.vec(len(p.ctx.typeCtx), func(w *writer, i int) {
		ft := p.ctx.typeCtx[i].ft
		w.byte1(tagFunc)
		w.vec(len(ft.params), func(w *writer, j int) { w.valType(ft.params[j]) })
		w.vec(len(ft.results), func(w *writer, j int) { w.valType(ft.results[j]) })
	})
}

// encodeImports writes the import section from the retained imports (encode.ml:938-943).
//
// Each entry is `name name externtype`, and the descriptor's five forms are encode.ml:202-208 — a
// kind byte then that kind's own payload. Nothing here branches on *spelling*: an inline-imported
// memory and an `(import … (memory …))` are the same `textImport` by the time they arrive, which is
// the point of both arms calling `defineImport`.
func (p *parser) encodeImports(w *writer) {
	w.vec(len(p.ctx.imports), func(w *writer, i int) {
		im := p.ctx.imports[i]
		w.name(im.module)
		w.name(im.name)
		w.byte1(externKindByte(im.desc.kind))
		switch im.desc.kind {
		case importFunc:
			w.u32(im.desc.typeIdx)
		case importTag:
			// The attribute byte, `u32 0x00l` at encode.ml:191 — a *zero LEB*, not a raw byte, and
			// the decoder reads it as one (sections.go's tag arm). One value is legal today
			// (`exnref`'s attribute), so writing 0 is the whole domain rather than a default.
			w.u32(0)
			w.u32(im.desc.typeIdx)
		case importTable:
			t := im.desc.table
			w.valType(t.elem)
			w.limits(t.addr64, t.lim)
		case importMemory:
			w.limits(im.desc.mem.addr64, im.desc.mem.lim)
		case importGlobal:
			g := im.desc.global
			w.valType(g.val)
			w.mutability(g.mut)
		}
	})
}

// encodeExports writes the export section from the retained exports (encode.ml:1009-1014).
//
// Each entry is `name externidx`, and `externidx`'s five forms are :1001-1007 — a kind byte then the
// index. **The same five bytes in the same order as `externtype`'s** (:202-208), which is why
// `externKindByte` is called here rather than copied: an export's kind byte and an import's are one
// fact, and re-deriving it is how #105 happened. TestExternKindByteAgreesForBothSections holds it.
//
// Simpler than encodeImports because there is no payload to branch on — an export names a thing that
// is already defined, where an import declares its whole type. So the kind byte's five arms collapse
// to `w.u32(ex.idx)` for all of them.
func (p *parser) encodeExports(w *writer) {
	w.vec(len(p.ctx.exports), func(w *writer, i int) {
		ex := p.ctx.exports[i]
		w.name(ex.name)
		w.byte1(externKindByte(ex.kind))
		w.u32(ex.idx)
	})
}

// externKindByte maps a retained import's kind to its binary kind byte.
//
// **A mapping and not a cast, and no arm of it is an identity.** `importKind`'s values are ordered by
// the reference's *message* table — tag, global, memory, table, function (context.go:493,
// parser.mly:1322-1354) — while the binary kind bytes are func 0x00, table 0x01, memory 0x02, global
// 0x03, tag 0x04 (encode.ml:202-208). Those two orders are exact reversals, so a `byte(kind)` would
// write 0x00 for a tag and 0x04 for a func, and every byte it writes is a *legal* kind byte.
//
// **What the cast actually does was probed rather than asserted, and the first draft of this comment
// was wrong about it.** The draft claimed the image would "decode clean as a different module" — the
// accept-direction class. Substituting `byte(im.desc.kind)` and running the round trip gives 16 fails
// and 8 passes, and the numbers are the interesting part:
//
//   - all 16 failures are the *decoder* rejecting, not the want column disagreeing. A kind byte is
//     followed by that kind's payload, so a wrong byte points the decoder at the wrong payload grammar
//     and it usually notices: `malformed reference type: 0x7f` for a global written as a table,
//     `unexpected end of section` for a func written as a tag.
//   - the 8 passes are **every memory row and only the memory rows**, because memory is the fixed
//     point of a five-element reversal. The cast writes 0x02 for it and 0x02 is right.
//   - the near miss is a table written as a global: `03 70 00 01` reads as a perfectly plausible
//     `funcref` const global, and what caught it was the **section size mismatch** on the leftover
//     byte, not the payload grammar.
//
// So the round trip does catch this, but it catches it through the payloads, which is a property of
// today's five payload grammars happening to differ — not a structural guard. Two kinds with the same
// payload shape would swap silently. The mapping is the guard; the round trip is the witness that it
// is installed.
//
// It reads `binary`'s own constants rather than literals, so the two sides of the round trip cannot
// disagree about a number: this is the same one-concept-one-trigger argument `valTypeByte` makes.
func externKindByte(k importKind) byte {
	switch k {
	case importFunc:
		return byte(binary.ExternFunc)
	case importTable:
		return byte(binary.ExternTable)
	case importMemory:
		return byte(binary.ExternMemory)
	case importGlobal:
		return byte(binary.ExternGlobal)
	case importTag:
		return byte(binary.ExternTag)
	}
	// Unreachable: importKind has five values and all five are above. A panic rather than a zero,
	// because a zero is `ExternFunc` — a plausible wrong byte, which is grave #36's class moved into
	// an image where no oracle reads it. `valType`'s panic carries the same argument at more length.
	panic(fmt.Sprintf("text: unencodable import kind %d reached the emitter", int(k)))
}

// mutability writes a globaltype's mutability byte: `Cons -> 0 | Var -> 1` (encode.ml:104-106).
//
// On `writer` for the reason `limits` is: it is a byte-layer fact, and the reader it inverts is
// `decodeGlobalType`'s. One byte, and the direction matters — writing 1 for immutable would make
// every `(import "m" "g" (global i32))` in the corpus a mutable global, which decodes clean.
func (w *writer) mutability(mut bool) {
	if mut {
		w.byte1(1)
		return
	}
	w.byte1(0)
}

// encodeTables writes the table section from the retained definitions.
//
// **The field order is the binary format's, not the text's**, and they differ: text is `addrtype
// limits reftype` (parser.mly:460) while binary is `reftype` then `limits` (encode.ml:200). Emitting
// in text order would produce an image whose first byte the decoder reads as a limits flags byte —
// for `(table 1 funcref)` that is `0x70`, `malformed limits flags`, which is the *lucky* failure. The
// unlucky one is a reftype byte that happens to be a legal flags value: `funcref`'s 0x70 is not, but
// a future one-byte type could be, and then the image decodes clean as a different table.
func (p *parser) encodeTables(w *writer) {
	w.vec(len(p.ctx.tabDefs), func(w *writer, i int) {
		t := p.ctx.tabDefs[i]
		w.valType(t.elem)
		w.limits(t.addr64, t.lim)
	})
}

// encodeMemories writes the memory section from the retained definitions.
func (p *parser) encodeMemories(w *writer) {
	w.vec(len(p.ctx.memDefs), func(w *writer, i int) {
		m := p.ctx.memDefs[i]
		w.limits(m.addr64, m.lim)
	})
}

// limits writes a limits: the flags byte, the minimum, and the maximum when present.
//
// The flags are `flag (max <> None) 0 + flag (at = I64AT) 2` (encode.ml:187) — bit 0 for a maximum,
// bit 2 for a 64-bit address type. **Bit 1 is the shared flag and this never sets it**, which is not
// an omission: the text grammar's `limits` has no `shared` arm (parser.mly:466-468), so no wat source
// can denote a shared memory and an encoder that could emit one would be encoding something no input
// says. The decoder reads 0x02/0x03 behind the Threads gate, and a threads-era text grammar will add
// the arm; until then the bit has no source.
//
// It is a method on `writer` rather than a function in encode.go because it is the byte layer's kind
// of fact — a flags byte and two LEBs — and because the *reader* it inverts is `decodeLimits`. Its
// falsification is the round trip, per writer.go's header.
func (w *writer) limits(addr64 bool, lim limits) {
	var flags byte
	if lim.hasMax {
		flags |= 0x01
	}
	if addr64 {
		flags |= 0x04
	}
	w.byte1(flags)
	w.u64(lim.min)
	if lim.hasMax {
		w.u64(lim.max)
	}
}

// valTypeByte is the encoding of one resolved value type, and the encodability predicate.
//
// **One function for both questions on purpose.** A separate "can I encode this" check would be a
// second place knowing the same table, and the two would drift — with the failure mode being an
// emitter reached with a type it has no byte for, resolving to a plausible wrong byte. Returning
// `(byte, bool)` makes the frontier check and the emitter the same fact asked twice.
//
// The number types key on their *spelling*, which is what `resolvedVal.num` holds and is not a
// stand-in: the reference's lexer collapses the four to one NUMTYPE class and keeps the payload, so
// the spelling *is* the value (typetable.go's comment).
//
// The reference cases are the two Wasm 2.0 forms, and the `null` field is part of the key rather
// than decoration. `funcref` and `(ref null func)` both resolve to `{null: true, abs: kwFunc}` —
// the parser normalizes the abbreviation, so 0x70 is lossless for both. `(ref func)` is
// `{null: false, …}`, a *different type*, encoded `64 70` with GC's non-null prefix; writing 0x70
// for it would emit a nullable type where the text said non-nullable, which is a wrong module that
// decodes clean. Hence the null check, and hence the twelve GC heap types and every `isIdx` form
// returning false: they need the 0x63/0x64 prefix, which `decodeRefType` declines with the GC gate
// off, so a module holding one is refused rather than mis-encoded.
func valTypeByte(v resolvedVal) (byte, bool) {
	if v.num != "" {
		switch v.num {
		case "i32":
			return byte(binary.I32), true
		case "i64":
			return byte(binary.I64), true
		case "f32":
			return byte(binary.F32), true
		case "f64":
			return byte(binary.F64), true
		case "v128":
			return byte(binary.V128), true
		}
		return 0, false
	}
	if v.isIdx || !v.null {
		return 0, false
	}
	switch v.abs {
	case kwFunc:
		return byte(binary.FuncRef), true
	case kwExtern:
		return byte(binary.ExternRef), true
	default:
		// A real fallback rather than a shrug, which is what `exhaustive`'s
		// `default-signifies-exhaustive` is asking about: the two arms above are the *entire*
		// ungated reference set, so everything else is the frontier by construction, and one
		// answer is the correct answer for all of it. Enumerating the other ten heap types
		// would be ten arms with identical bodies that go stale the day an eleventh is added —
		// deriving the domain instead of listing it, the same reason `heapWat`'s control ranges
		// over `absoluteHeaptypes`. `TestEncodeRefusesWhatItCannotWrite` covers this path.
		return 0, false
	}
}

// valType writes one resolved value type.
//
// Unreachable with an unencodable type, because `encodableOrErr` asked `valTypeByte` the same
// question first — and the panic is *not* a precondition excusing its own check: it fires only if
// the two callers of one function disagree, which cannot happen, and it says that rather than
// pretending to validate. The alternative is writing a plausible byte, which is grave #36's class
// relocated from a message into an image, where no oracle reads it.
func (w *writer) valType(v resolvedVal) {
	b, ok := valTypeByte(v)
	if !ok {
		panic("text: unencodable value type " + v.String() + " reached the emitter, which means " +
			"encodableOrErr and valType disagree about a table they share")
	}
	w.byte1(b)
}
