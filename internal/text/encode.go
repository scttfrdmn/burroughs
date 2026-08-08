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

// The section ids this emitter writes. The remaining eight arrive with their sections — naming them
// now would be constants nothing reads, which is the placeholder shape #6 rules on, and the honest
// version of "later" here is an empty line rather than a tracked stub.
//
// **The numbers are ids, and ids are not the order they are written in.** `module_` writes section 12
// *before* section 10 and section 11 last (encode.ml:1156-1161), which is not a quirk of the writer
// but the format: `binary.wast:1194` asserts a code section preceding a data count section is
// malformed. `sectionRank` on the decoding side holds the same fact as an explicit rank, and
// `encode`'s call sequence is where this side holds it.
const (
	secType      byte = 1
	secImport    byte = 2
	secFunction  byte = 3
	secTable     byte = 4
	secMemory    byte = 5
	secGlobal    byte = 6
	secExport    byte = 7
	secElem      byte = 9
	secCode      byte = 10
	secData      byte = 11
	secDataCount byte = 12
)

// tagFunc, tagStruct and tagArray are `comptype`'s three forms — 0x60/-0x20, 0x5f/-0x21,
// 0x5e/-0x22 read as the sleb(7) the decoder reads them as (decodeCompType's comment has the
// vector that forces the signedness, and its `-0x21`/`-0x22` case arms are the already-merged
// decoder-side authority these three bytes are verified against). Written as bytes because that
// is what a minimal sleb(7) of each negative form is.
const (
	tagFunc   byte = 0x60
	tagStruct byte = 0x5f
	tagArray  byte = 0x5e
)

// packI8 and packI16 are `packtype`'s two forms — 0x78/-0x08, 0x77/-0x09 — folded to bytes the
// same way `absoluteHeaptypeBytes`'s comment folds every other negative-s7 form this package
// writes, and verified against `decodeStorageType`'s own two cases (sections.go, already merged).
const (
	packI8  byte = 0x78
	packI16 byte = 0x77
)

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
	// Section 6 between memory and export, which is both `module_`'s order (encode.ml:1145-1150) and id
	// order — the tag section sits between them in the reference and has no emitter here, so its absence
	// changes nothing about the two neighbours.
	if len(p.ctx.globalDefs) > 0 {
		w.section(secGlobal, p.encodeGlobals)
	}
	if len(p.ctx.exports) > 0 {
		w.section(secExport, p.encodeExports)
	}
	// Section 9 sits between export and data count, which is `module_`'s order (encode.ml:1150-1161)
	// and here happens to be id order too — the two places the format departs from it are 12-before-10
	// and 11-after, below.
	if len(p.ctx.elemDefs) > 0 {
		w.section(secElem, p.encodeElems)
	}
	// **Section 12 before section 10, and section 11 after it** — `module_`'s order (encode.ml:1156-1161),
	// which is the format's rather than the writer's: a code section preceding a data count section is
	// malformed (`binary.wast:1194`), because the count is what lets a body's `data.drop` be validated
	// before the segments are read. Writing them in id order would emit an image this project's own
	// decoder rejects.
	//
	// Section 12's *condition* is `p.ctx.sawDataRef`, not `len(dataDefs) > 0` — see that field's
	// comment for the two directions in which the obvious test is wrong.
	if p.ctx.sawDataRef {
		w.section(secDataCount, p.encodeDataCount)
	}
	if len(funcs) > 0 {
		writeCodeSection(&w, funcs)
	}
	if len(p.ctx.dataDefs) > 0 {
		w.section(secData, p.encodeDatas)
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
			"the type, import, function, table, memory, export, element, code, data and data count "+
			"sections (#8)", p.ctx.firstNonType.Text)
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
		// **The global row can fire, and unlike the func row's it does so on a spelling that exists.**
		// `globalField` has two arms — the defining one, which reaches `defineGlobal`, and the inline-import
		// one, which returns through `inlineImportTail` and records nothing here — so a third arm, or a
		// mis-placed `noteDefined`, is a section 6 short by one entry. Falsified by deleting the
		// `defineGlobal` call: `(module (global i32 (i32.const 0)))` then emits an 8-byte bare preamble and
		// this fires with `0 global definitions retained, 1 defined`.
		{importGlobal, len(p.ctx.globalDefs)},
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
	// The same check for section 11, and it fires today: `datasSeen` is the grammar's, incremented in
	// `noteData` at each of the two `(data` recognizers, and `len(dataDefs)` is the emitter's, appended
	// by `defineData`. Falsified by deleting the `defineData` call from `memoryDataSugar`:
	// `(module (memory (data "abc")))` then emits a memory of one page with no bytes in it — a module
	// that decodes clean and whose memory is empty — and this fires with `0 data segments retained,
	// 1 parsed`. That is exactly the defect the sugar arm carried while section 11 did not exist, which
	// was invisible because nothing compared the two numbers.
	if got, want := len(p.ctx.dataDefs), p.ctx.datasSeen; got != want {
		return fmt.Errorf("text: internal: %d data segments retained, %d parsed — a spelling withdrew "+
			"the encoder's frontier without recording its content (#8)", got, want)
	}
	// The same check for section 9, and it fires today for section 11's reason: `elemsSeen` is the
	// grammar's, incremented in `noteElem` at the `(elem` recognizer, and `len(elemDefs)` is the
	// emitter's, appended by `defineElem`. The two numbers come from different code, so a spelling that
	// parses a segment without retaining it is a section 9 short by one entry.
	//
	// **`tableElemSugar` is the reason this is a live control rather than a tripwire.** That arm defines
	// a table *sized from* an element segment and is the sixth `(elem` in the grammar — the one that is
	// not an `elem` field — so it must both `noteElem` and `defineElem`, and its size arithmetic has no
	// source token to be wrong about. Falsified by deleting its `defineElem` call:
	// `(module (table funcref (elem 0)))` then emits a table of one element with nothing in it — a
	// module that decodes clean and whose table is all nulls — and this fires with `0 element segments
	// retained, 1 parsed`.
	if got, want := len(p.ctx.elemDefs), p.ctx.elemsSeen; got != want {
		return fmt.Errorf("text: internal: %d element segments retained, %d parsed — a spelling "+
			"withdrew the encoder's frontier without recording its content (#8)", got, want)
	}
	// An element segment's type is the same frontier as a table's: `(elem (ref func) (ref.func 0))` and
	// `(elem externref …)` need bytes `valTypeBytes` does not have, and `elem.wast` writes both. Asked
	// through the one predicate so this cannot disagree with what `encodeElems` will write.
	//
	// **The exemption is the writer's family test verbatim, and it has to be — grave #146.** A segment
	// taking one of the four *index* forms writes an `elemkind` byte instead of a reftype, so its type
	// needs no `valTypeBytes` entry; every other segment writes a reftype and does. Which family a
	// segment takes is `isElemKind() && allElemIndex()` in `encodeElems`, and this exemption asked only
	// the first half — so a `(ref func)` segment whose elements fail `is_elem_index` was exempted here
	// and then routed to the *expression* family by the writer, which called `w.valType` on a type
	// `valTypeBytes` refuses. Not a wrong image: a **panic**, out of the arm whose whole job is to say
	// the two callers cannot disagree, on three spellings the grammar admits
	// (`(elem (ref func) (item (ref.func 0) (ref.func 0)))` and its active and declarative twins).
	//
	// The lesson is the one this file already states as *one concept, one trigger* and got wrong by
	// stating the concept twice: the exemption is not "segments that pass `isElemKind`", it is
	// "segments the writer routes to the index family", and only the writer's own conjunction says
	// which those are. A predicate reconstructed from one of two conditions is the under-matching
	// trigger defect (#78) in a skip rather than in a guard, and it fails the same way — silently,
	// producing no finding, until the population it wrongly exempts is actually reached.
	//
	// What kept it quiet: the two conditions differ only on a segment that is `(ref func)`-typed *and*
	// holds an element that is not exactly `ref.func x`, and the eleven-row arm table one file over
	// spells `(ref func)` only with bare-index elements. Found by enumerating mode × reftype × element
	// shape rather than by reading — TestEncodeMatchesTheReferenceOnElemFlags, which is what that
	// enumeration became.
	for i, e := range p.ctx.elemDefs {
		if e.isElemKind() && e.allElemIndex() {
			continue // the index family: writes an elemkind byte, not a reftype
		}
		if _, ok := valTypeBytes(e.elemType); !ok {
			return fmt.Errorf("cannot yet encode element segment %d: element type %s needs a "+
				"parameterized reference encoding, which arrives with the GC gate (#8)", i, e.elemType)
		}
	}
	for i, t := range p.ctx.tabDefs {
		if _, ok := valTypeBytes(t.elem); !ok {
			return fmt.Errorf("cannot yet encode table %d: element type %s needs a parameterized "+
				"reference encoding, which arrives with the GC gate (#8)", i, t.elem)
		}
	}
	// A defined global's type is the same frontier, and it is the *widest* of the three: a globaltype's
	// valtype is any valtype at all — `(global anyref (ref.null any))` and `(global (ref func) …)` are
	// both well-formed text — where a table's is a reftype and an element segment's likewise. Asked
	// through `valTypeBytes`, the one predicate, so this cannot disagree with what `encodeGlobals` writes.
	//
	// The *initializer* needs no check here: its instructions are refused at the cursor by
	// `refuseUnencodable`, which is where an unencodable instruction has a token to point at, and the
	// frontier record it sets is what the `haveNonType` check above reports. So the two halves of a
	// global are refused by two different mechanisms, and that is the same division the code section uses
	// — a func's signature is checked in the type loop below and its body at the cursor.
	for i, g := range p.ctx.globalDefs {
		if _, ok := valTypeBytes(g.typ.val); !ok {
			return fmt.Errorf("cannot yet encode global %d: type %s needs a parameterized reference "+
				"encoding, which arrives with the GC gate (#8)", i, g.typ.val)
		}
	}
	// An import's descriptor can hold a valtype in two of its five forms, and both are the same
	// frontier as a table definition's element type: `(import "m" "t" (table 1 (ref func)))` needs
	// GC's 0x64 prefix. Asked through `valTypeBytes`, the one predicate, so this cannot disagree with
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
		if _, ok := valTypeBytes(v); !ok {
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
		switch ct.kind {
		case compFunc:
			for _, group := range [][]resolvedVal{ct.ft.params, ct.ft.results} {
				for _, v := range group {
					if _, ok := valTypeBytes(v); !ok {
						return fmt.Errorf("cannot yet encode type %d: %s needs a parameterized "+
							"reference encoding, which arrives with the GC gate (#8)", i, v)
					}
				}
			}
		case compStruct, compArray:
			// A struct's or array's fields are retained as of decision 0021, and every field's
			// storage is either a packed width (always encodable — its two wire bytes are fixed,
			// `-0x08`/`-0x09`) or a full value type, whose encodability is `valTypeBytes`' existing
			// question — the same frontier a functype's param meets above, asked through the same
			// predicate so the two cannot disagree about what is implemented.
			for j, f := range ct.fields {
				if f.storage.packed {
					continue
				}
				if _, ok := valTypeBytes(f.storage.val); !ok {
					return fmt.Errorf("cannot yet encode type %d field %d: %s needs a "+
						"parameterized reference encoding, which arrives with the GC gate (#8)",
						i, j, f.storage.val)
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
//
// **Three arms since decision 0021**, one per `compKind` — `comptype`'s own three productions
// (decode.ml:250-259). A struct writes its tag then `vec(fieldtype)`, count then each field; an
// array writes its tag then exactly *one* bare fieldtype, no count — the arity `decodeCompType`'s
// already-merged array branch reads on the other side of this round trip, and getting it wrong
// (writing a vector for an array, or a bare fieldtype for a struct) produces a well-formed image
// denoting a different composite type, decodable and wrong.
func (p *parser) encodeTypes(w *writer) {
	w.vec(len(p.ctx.typeCtx), func(w *writer, i int) {
		ct := p.ctx.typeCtx[i]
		switch ct.kind {
		case compFunc:
			w.byte1(tagFunc)
			w.vec(len(ct.ft.params), func(w *writer, j int) { w.valType(ct.ft.params[j]) })
			w.vec(len(ct.ft.results), func(w *writer, j int) { w.valType(ct.ft.results[j]) })
		case compStruct:
			w.byte1(tagStruct)
			w.vec(len(ct.fields), func(w *writer, j int) { w.fieldType(ct.fields[j]) })
		case compArray:
			w.byte1(tagArray)
			// Exactly one field, per arraytype's own arity (comptype parses it that way, and
			// `resolveFields` never changes a slice's length) — bare, no count.
			w.fieldType(ct.fields[0])
		}
	})
}

// fieldType writes one resolved field: its storage type, then its mutability byte
// (`FieldT (mut, t) -> storagetype t; mutability mut`, encode.ml:169-170).
func (w *writer) fieldType(f resolvedField) {
	w.storageType(f.storage)
	w.mutability(f.mut)
}

// storageType writes one resolved storage type: a full value type through `valTypeBytes` — the
// same predicate `encodableOrErr` asked, so the two cannot disagree about what is implemented —
// or one of the two packed forms, which always has a byte (`packtype`'s domain is exactly `{i8,
// i16}`, decode.ml:236-241, so there is no third packed spelling to be unencodable).
func (w *writer) storageType(st resolvedStorage) {
	if !st.packed {
		w.valType(st.val)
		return
	}
	switch st.width {
	case 8:
		w.byte1(packI8)
	case 16:
		w.byte1(packI16)
	default:
		// Unreachable: storagetype's parse arm sets width to 8 or 16 unconditionally
		// (types.go's storagetype), and nothing downstream changes it. A panic rather than a
		// plausible byte, for `externKindByte`'s reason — a wrong packed byte here writes a
		// well-formed module denoting a different storage width, invisible to any oracle that
		// stops at this function's return.
		panic(fmt.Sprintf("text: unencodable packed storage width %d reached the emitter", st.width))
	}
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
// disagree about a number: this is the same one-concept-one-trigger argument `valTypeBytes` makes.
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

// encodeGlobals writes section 6: one entry per *defined* global, in source order (#8).
//
// `globaltype gt; const c` (encode.ml:991-993), which is three bytes' worth of decisions and no arms:
// no mode flag, no index, and no family choice — section 6 is the simplest of the segment-shaped
// sections precisely because a global is not a segment.
//
// **The field order is `valtype` then `mutability`** (`GlobalT (mut, t) -> valtype t; mutability mut`,
// :193-194), which is the reverse of the OCaml constructor's own argument order and the reverse of the
// text's `(mut i32)` spelling. So the tell for getting it wrong is the same one `encodeTables`
// records: the decoder reads the first byte as a valtype, and `00`/`01` is `malformed value type`
// rather than a plausible other module. The lucky failure — but `w.mutability` writing the wrong value
// is the unlucky one, which is why that byte has its own function with its own direction argument.
//
// Cannot fail, on every other section's discipline: `defineGlobal`'s thunk resolved the type and
// encoded the initializer in stage 2, and `encodableOrErr` refused anything left.
func (p *parser) encodeGlobals(w *writer) {
	w.vec(len(p.ctx.globalDefs), func(w *writer, i int) {
		g := p.ctx.globalDefs[i]
		w.valType(g.typ.val)
		w.mutability(g.typ.mut)
		w.bytes(g.init)
	})
}

// encodeMemories writes the memory section from the retained definitions.
func (p *parser) encodeMemories(w *writer) {
	w.vec(len(p.ctx.memDefs), func(w *writer, i int) {
		m := p.ctx.memDefs[i]
		w.limits(m.addr64, m.lim)
	})
}

// encodeDatas writes section 11: one entry per data segment, in source order (#8).
//
// The three arms are `encode.ml`'s `data` (:1092-1101), and the discriminator is the **resolved**
// memory index rather than the text's spelling:
//
//	Passive                 -> 0x01, payload
//	Active ({it = 0l}, c)   -> 0x00, const expr, payload
//	Active (x, c)           -> 0x02, memory index, const expr, payload
//
// So `(data (memory 0) …)` and `(data …)` produce identical bytes, which is what every other producer
// does and what `textData`'s comment argues at length. The fourth arm, `Declarative`, is an *error* in
// the reference (`illegal declarative data segment`) and is unreachable here for a stronger reason
// than a check: the text grammar's `data` has no declarative arm at all (parser.mly:1094-1105) — only
// `elem` does — so there is no spelling to refuse.
//
// **The mode flag is a `u32`, not a byte.** `u32 0x01l` (:1095) means a segment flag above 127 would
// take two bytes; no such flag exists, so this is a distinction without a difference *today*, and
// writing `byte1` would be a second reading of the same field to keep in agreement later. Same
// argument `opBytes` makes about a prefixed opcode's sub-opcode, which is where getting it wrong
// would have mattered.
//
// Cannot fail, on every other section's discipline: `defineData`'s thunks resolved the index and
// encoded the offset in stage 2, and `encodableOrErr` refused anything left.
func (p *parser) encodeDatas(w *writer) {
	w.vec(len(p.ctx.dataDefs), func(w *writer, i int) {
		d := p.ctx.dataDefs[i]
		switch {
		case d.passive:
			w.u32(0x01)
		case d.mem == 0:
			w.u32(0x00)
			w.bytes(d.offset)
		default:
			w.u32(0x02)
			w.u32(d.mem)
			w.bytes(d.offset)
		}
		w.byteVec(d.bytes)
	})
}

// encodeElems writes section 9: one entry per element segment, in source order (#8).
//
// **Eight arms, and the choice between the two families of four is derived from the segment's
// *content* rather than from the text's spelling** — `textElem`'s comment carries that derivation and
// this is the half that acts on it:
//
//	if is_elem_kind rt && List.for_all is_elem_index cs then  (* index forms   0/1/2/3 *)
//	else                                                      (* expression forms 4/5/6/7 *)
//
// `is_elem_kind rt` is `rt = (NoNull, FuncHT)` (encode.ml:1044-1046), a question about the reftype's
// **nullability** — so `funcref` fails it and `(ref func)` passes, and the two spellings of what looks
// like one type take different flags. `is_elem_index` is `[{it = RefFunc _}]` (:1052-1055), a question
// about each element expression's **shape**, folded with `for_all`; `resolvedElem.funcs` holds the
// per-element answer because a segment may mix `(ref.func 0)` with `(ref.null func)` and
// `bulk.wast:12` does exactly that.
//
// Then the mode selects within the family, on the **resolved** table index rather than on whether the
// text wrote a `(table …)` — `encodeDatas`' discriminator, for its reason:
//
//	index      forms: Passive 0x01 · Active 0 0x00 · Active x 0x02 · Declarative 0x03
//	expression forms: Passive 0x05 · Active 0 0x04 · Active x 0x06 · Declarative 0x07
//
// **0x04 carries a guard the other seven do not, and dropping it writes a wrong module that decodes
// clean.** The reference's arm is `Active ({it = 0l}, c) when rt = (Null, FuncHT)` (:1079), so the
// short active form is available only to a segment whose element type is exactly `funcref` — every
// other reftype at table 0 falls through to `Active (x, c)` and writes 0x06, table index and all.
// That is not redundancy: 0x04's payload has no reftype field, so a `(elem (i32.const 0) externref
// (ref.null extern))` written as 0x04 would decode as a *funcref* segment. The fall-through is what
// the reference means by putting the guard on the arm rather than on the family.
//
// **wabt agrees with the reference, and the two false claims this paragraph carried before are worth
// keeping as the record of how a producer comparison goes wrong.** wabt 1.0.41 `--enable-all` writes
// `09 07 01 05 70 01 d2 00 0b` for `(module (func) (elem funcref (ref.func 0)))` — `05 70 …`, the
// reference's expression form to the byte — and takes the index form for `(elem (ref func) …)`, which
// is right because `(ref func)` is `(NoNull, FuncHT)` and `is_elem_kind` is a question about
// nullability.
//
// The first version argued the flag choice *from* "every other producer including wabt". The second
// recorded the opposite, "all five `funcref`-spelled expression forms disagree … 452 of the corpus's
// 1383 `(elem` forms are on that divergence", and claimed wabt rejects `(elem (ref func) …)` outright.
// The second was measured, which is why it was believed — **and it was measured with the proposals
// off**, where wabt has no funcref/`(ref func)` distinction to make: it collapses `funcref` to the
// index form and rejects `(ref func)` as unrecognized syntax. Re-run with `--enable-all`, all three
// spellings agree. A feature-gate artefact was read as a producer disagreement, and a corpus count
// was computed on top of it.
//
// Both errors point the same way — *the measurement was of a differently-configured instrument* — and
// neither ever bore on the choice, since the reference implementation is the authority for what the
// bytes mean whatever wabt does. That is the lesson worth the space: the first claim was
// corroborating decoration on a conclusion that stands without it, the second was corroborating
// decoration with the sign flipped, and a fact nothing rests on gets stated as confidently as one
// that carries weight. So #67's corpus comparator is expected to *agree* on section 9, and a
// disagreement there is a finding rather than a known style difference.
//
// Cannot fail, on every other section's discipline: `defineElem`'s thunks resolved the table index and
// encoded the offset and every element in stage 2, and `encodableOrErr` refused what has no byte.
func (p *parser) encodeElems(w *writer) {
	w.vec(len(p.ctx.elemDefs), func(w *writer, i int) {
		e := p.ctx.elemDefs[i]
		if e.isElemKind() && e.allElemIndex() {
			switch {
			case e.mode == elemPassive:
				w.u32(0x01)
				w.elemKind(e.elemType)
			case e.mode == elemDeclarative:
				w.u32(0x03)
				w.elemKind(e.elemType)
			case e.table == 0:
				w.u32(0x00)
				w.bytes(e.offset)
			default:
				w.u32(0x02)
				w.u32(e.table)
				w.bytes(e.offset)
				w.elemKind(e.elemType)
			}
			w.vec(len(e.funcs), func(w *writer, j int) { w.bytes(e.funcs[j].imm) })
			return
		}
		switch {
		case e.mode == elemPassive:
			w.u32(0x05)
			w.valType(e.elemType)
		case e.mode == elemDeclarative:
			w.u32(0x07)
			w.valType(e.elemType)
		case e.table == 0 && e.elemType.isFuncref():
			w.u32(0x04)
			w.bytes(e.offset)
		default:
			w.u32(0x06)
			w.u32(e.table)
			w.bytes(e.offset)
			w.valType(e.elemType)
		}
		w.vec(len(e.exprs), func(w *writer, j int) { w.bytes(e.exprs[j]) })
	})
}

// elemKind writes the `elemkind` byte, which is 0x00 and is *not* a reftype byte.
//
// `elem_kind` is `function (NoNull, FuncHT) -> byte 0x00 | _ -> assert false` (encode.ml:1048-1050) —
// so the byte is a constant and the function exists to assert its precondition. Kept as a method with
// the argument rather than inlined as `w.byte1(0x00)` for exactly the reason the reference keeps the
// match: the four index-form arms reach it only under `is_elem_kind`, and 0x00 written for any other
// reftype is a segment claiming to hold funcrefs. The panic is `valType`'s: a disagreement between the
// arm selection and this precondition is an internal inconsistency, not a bad input.
//
// Note which field this is: the index forms carry an *elemkind* where the expression forms carry a
// *reftype*, two different one-byte encodings of the same idea at the same offset — `funcref` is 0x70
// as a reftype and 0x00 as an elemkind. Writing one where the other belongs is a segment whose element
// type decodes to something the text never said, which is why `w.elemKind` and `w.valType` are
// separate methods and each arm above names the one its form takes.
func (w *writer) elemKind(v resolvedVal) {
	if v.null || v.abs != kwFunc || v.isIdx || v.num != "" {
		panic("text: elemKind called for " + v.String() + ", which is not (NoNull, FuncHT) — the " +
			"index-form arms are guarded by is_elem_kind and this is that guard's other half")
	}
	w.byte1(0x00)
}

// encodeDataCount writes section 12: the segment count and nothing else.
//
// `section 12 len (List.length datas)` (encode.ml:1109) — `len` is a bare `u32`, *not* a vector, so
// this is one LEB with no elements after it. The count is the number of **segments**, while whether
// the section exists at all is a question about **instructions** (`sawDataRef`), and conflating those
// two is the defect that field's comment measures.
func (p *parser) encodeDataCount(w *writer) {
	w.u32(uint32(len(p.ctx.dataDefs)))
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

// valTypeBytes is the encoding of one resolved value type, and the encodability predicate.
//
// **One function for both questions on purpose.** A separate "can I encode this" check would be a
// second place knowing the same table, and the two would drift — with the failure mode being an
// emitter reached with a type it has no byte for, resolving to a plausible wrong byte. Returning
// `([]byte, bool)` makes the frontier check and the emitter the same fact asked twice.
//
// **A byte slice, not a byte, since decision 0018's encoder-side implementation.** A `reftype` has
// two shapes of encoding, and only one of them fits a single byte: the *nullable abstract*
// abbreviations (`funcref`, `externref`, and the ten further GC forms — twelve single bytes
// total, per `decodeRefType`'s `-0x10`/`-0x11` and `-0x0C..-0x0F`/`-0x12..-0x17` arms). Every other
// form the parser can now resolve — a non-null abstract form (`(ref i31)`, no bare byte exists for
// it) and the indexed form at either nullability (`(ref $t)`/`(ref null $t)`, ditto) — needs the
// general parameterized production: a prefix byte (`0x64` for `(ref ht)`, `0x63` for
// `(ref null ht)` — `decodeRefType`'s `-0x1C`/`-0x1D`, folded to bytes) followed by `heaptype`'s own
// bytes, which `heapTypeBytes` already writes correctly and unchanged by this decision (#8's
// existing frontier). The rule is read off `decodeRefType`/`decodeHeapType` (sections.go),
// the decoder side's already-merged authority, not re-derived: verified against real wire bytes
// through the fixed decoder before this function was written (`(ref i31)`, `(ref $t)`/`(ref null
// $t)` both nullabilities, `funcref` bare, `(ref func)` explicit).
//
// The number types key on their *spelling*, which is what `resolvedVal.num` holds and is not a
// stand-in: the reference's lexer collapses the four to one NUMTYPE class and keeps the payload, so
// the spelling *is* the value (typetable.go's comment).
//
// **The nullable-abstract/non-null-abstract split is on `null` alone, and that is grave #180's
// fix paying off here.** `funcref` and `(ref null func)` both resolve to `{null: true, abs:
// kwFunc}` — the parser normalizes the abbreviation — and both now correctly take the single-byte
// abbreviation `0x70`, matching `decodeRefType`'s own normalization (module.go's FuncRef/ExternRef
// comment). `(ref func)` is `{null: false, …}`, a *genuinely different type*, and takes the
// parameterized non-null form `64 70` — before #181's fix this distinction was not yet
// representable on the decode side either, which is why
// `TestParameterizedReferenceFormsRoundTrip` (encode_test.go) pins it directly, decoding both
// spellings through the fixed decoder and asserting `(ref func) != binary.FuncRef`.
func valTypeBytes(v resolvedVal) ([]byte, bool) {
	if v.num != "" {
		// Kind() is the accessor 0018 added for exactly this call shape: every one of the
		// five numeric/vector ValTypes has a raw wire byte and Kind() returns it unconverted,
		// keeping this arm's behavior identical to the pre-0018 byte(binary.I32) conversions
		// it replaces.
		switch v.num {
		case "i32":
			b, _ := binary.I32.Kind()
			return []byte{b}, true
		case "i64":
			b, _ := binary.I64.Kind()
			return []byte{b}, true
		case "f32":
			b, _ := binary.F32.Kind()
			return []byte{b}, true
		case "f64":
			b, _ := binary.F64.Kind()
			return []byte{b}, true
		case "v128":
			b, _ := binary.V128.Kind()
			return []byte{b}, true
		}
		return nil, false
	}
	if !v.isIdx && v.null {
		// The nullable abstract forms: the two Wasm 2.0 abbreviations and the ten further GC
		// ones, all twelve single bytes, all already in `absoluteHeaptypeBytes` — the same
		// table `heapTypeBytes` reads, kept as one table for the one-concept-one-trigger
		// reason that comment states.
		if b, ok := absoluteHeaptypeBytes[v.abs]; ok {
			return []byte{b}, true
		}
		return nil, false
	}
	// Everything else — a non-null abstract form, or the indexed form at either nullability —
	// needs the general parameterized production: the prefix byte `decodeRefType`'s `-0x1C`/
	// `-0x1D` arms read, then `heaptype`'s own bytes. `heapTypeBytes` already answers the second
	// half correctly for both the abstract and indexed cases (#8's existing frontier, unchanged
	// here); this function's new job is only the prefix.
	ht, ok := heapTypeBytes(v)
	if !ok {
		return nil, false
	}
	out := make([]byte, 0, 1+len(ht))
	if v.null {
		out = append(out, refPrefixNull)
	} else {
		out = append(out, refPrefixNonNull)
	}
	out = append(out, ht...)
	return out, true
}

// refPrefixNull and refPrefixNonNull are `reftype`'s two parameterized prefixes — `(ref null ht)`
// and `(ref ht)` respectively (decode.ml's `-0x1D`/`-0x1C`, folded to bytes exactly as
// `absoluteHeaptypeBytes`'s comment folds the abstract forms). Named rather than inlined because
// both `valTypeBytes` and its falsification test need to name "the wrong one" without recomputing
// the fold.
const (
	refPrefixNull    byte = 0x63 // -0x1D, `(ref null ht)`
	refPrefixNonNull byte = 0x64 // -0x1C, `(ref ht)`
)

// absoluteHeaptypeBytes is `heaptype`'s twelve keyword arms as the bytes encode.ml writes
// (:121-132), keyed by the kind `heaptype` returns.
//
// **A table here rather than in `internal/binary`, because ten of these have no exported
// byte constant over there** — `binary.ValType` represents every one of them as of 0018, but
// only FuncRef and ExternRef are named package-level values with a Kind() byte; the other ten
// are built by the decoder's own unexported refKind, so there is still no constant to
// reference for them and a `switch` would still be twelve arms hand-copied from the same
// place this map is. `FuncRef`/`ExternRef` are referenced rather than re-spelled precisely
// because they *do* exist over there, which keeps the two arms the corpus reaches honest against the
// decoder's own names.
//
// The bytes are the s7 encodings of the reference's negative forms: `s7 i` writes `i land 0x7f` for
// `-64 <= i < 64` (:59-61), so `-0x12` is `0x6e`. Written as the *byte* rather than as a signed value
// plus a conversion, for `ValType`'s stated reason — the encoding is the identity, and a second
// representation with a conversion between them is a second place to disagree.
//
// **Machine-checked against the authority**, by `TestAbsoluteHeaptypeBytesAgreeWithEncodeML`: a wrong
// byte here writes a *well-formed* module denoting a different type, which every `assert_malformed`
// in the corpus scores green by construction (§9 G-3), and the twelve differ from each other by one
// nibble. Hand-trusting a twelve-row table of adjacent bytes is the accept-direction hazard in its
// cheapest form — `authority-for-accept-direction-facts`, and `externKindByte`'s tripwire is the
// shape copied rather than re-derived.
var absoluteHeaptypeBytes = func() map[keywordKind]byte {
	funcByte, _ := binary.FuncRef.Kind()
	externByte, _ := binary.ExternRef.Kind()
	return map[keywordKind]byte{
		kwAny:      0x6e,       // -0x12
		kwEq:       0x6d,       // -0x13
		kwI31:      0x6c,       // -0x14
		kwStruct:   0x6b,       // -0x15
		kwArray:    0x6a,       // -0x16
		kwNone:     0x71,       // -0x0f
		kwFunc:     funcByte,   // -0x10
		kwNofunc:   0x73,       // -0x0d
		kwExn:      0x69,       // -0x17
		kwNoexn:    0x74,       // -0x0c
		kwExtern:   externByte, // -0x11
		kwNoextern: 0x72,       // -0x0e
	}
}()

// heapTypeBytes is the encoding of one resolved heap type, and its encodability predicate — the
// `heaptype` half of what `valTypeBytes` does for `reftype` (#8).
//
// **A heaptype is not a reftype, and the two agree byte for byte on twelve of thirteen forms**, which
// is the `elemKind`-versus-`valType` distinction one production lower down and is *worse* than a
// partial overlap. `reftype`'s twelve abbreviation arms are the same `s7` singletons this writes
// (encode.ml:137-148) and its general arms prefix `-0x1d`/`-0x1c` before recursing into `heaptype`
// (:150-151) — so `funcref` as a reftype is 0x70 and `func` as a heaptype is also 0x70. The two
// productions diverge on exactly two things: `null`, which a heaptype has no field for, and the
// *index* form, which `reftype` reaches only through a prefix and this writes bare. Two encodings
// agreeing on nearly everything is what makes calling the wrong one undetectable on a corpus.
//
// So the argument for a separate function is not the byte table, it is the **question**. `ref.null`'s
// immediate is a bare heaptype (`op 0xd0; heaptype t`, :414) with no nullability of its own — the
// instruction *is* the null — and routing it through `valTypeBytes` would ask a question with a `null`
// field the grammar never supplied. The value arrives as a `resolvedVal` because `resolveVal` is the
// one resolver, and `null` is whatever `heaptype`'s caller left there; this reads `abs`/`isIdx` and
// deliberately ignores it.
//
// # Bytes rather than a byte, and the frontier that closed
//
// **The index form is a `typeuse s33`, not a byte** (`UseHT ut -> typeuse s33 ut`, :133), so the
// return type is a slice — and that is what retired this function's old shape. It used to answer
// `(byte, bool)` with `func` and `extern` as the whole domain, which made `immHeaptype` the one entry
// in `encodableShapes` that was only *partly* encodable; the domain is now the entire production, and
// that map's comment records the change.
//
// The `bool` stays, and its one reachable cause is a **thirteenth heap type**: a kind added to
// `absoluteHeaptypes` (types.go) without a byte here. That is declared-and-tracked rather than
// unreachable-and-silent, and `TestEveryAbsoluteHeaptypeHasAByte` is what makes the omission a red
// board instead of a refused module — derived from the parser's own list, never from a second
// enumeration.
//
// **What the encoder writes is not what the default board accepts, and that is the layering working.**
// Ten of the twelve forms and every index form are GC's, so `decodeHeapType` declines them with a
// feature-named error while the gate is off (`sections.go`) — the module is *written* and then
// honestly *gated*, which is the ruling in "gates never manufacture malformedness" seen from the
// producing side. An encoder that refused them instead would be spoofing the decoder's configuration
// one layer up, and would leave the all-gates-on lane with nothing to answer on the merits.
func heapTypeBytes(v resolvedVal) ([]byte, bool) {
	if v.num != "" {
		// Unreachable from `heaptype`, which has no numtype arm, and answered rather than
		// panicked: `resolvedVal` is one type serving both productions, so this is the field that
		// says "you asked the reftype question here".
		return nil, false
	}
	if v.isIdx {
		return typeuseIdxBytes(v.idx), true
	}
	b, ok := absoluteHeaptypeBytes[v.abs]
	if !ok {
		return nil, false
	}
	return []byte{b}, true
}

// valType writes one resolved value type.
//
// Unreachable with an unencodable type, because `encodableOrErr` asked `valTypeBytes` the same
// question first — and the panic is *not* a precondition excusing its own check: it fires only if
// the two callers of one function disagree, which cannot happen, and it says that rather than
// pretending to validate. The alternative is writing a plausible byte, which is grave #36's class
// relocated from a message into an image, where no oracle reads it.
//
// **`w.bytes`, not `w.byte1`, since valTypeBytes widened**: most values still emit exactly one
// byte, but the parameterized forms emit a prefix plus heaptype's own bytes (up to several, for
// the indexed form's LEB), and `bytes` handles both without this method needing to know which case
// it is in.
func (w *writer) valType(v resolvedVal) {
	b, ok := valTypeBytes(v)
	if !ok {
		panic("text: unencodable value type " + v.String() + " reached the emitter, which means " +
			"encodableOrErr and valType disagree about a table they share")
	}
	w.bytes(b)
}
