package text

import "slices"

// The code section and its companion, the function section: #8's largest bucket and #7's door.
//
// # What this file adds that the rest of the parser refused to
//
// Every reader in instr.go is called for its *error* and its value discarded, which was correct
// while the product was a recognizer: 0011 makes `text.ReadModule` error-only by design, and a
// grammar that only answers yes/no needs nothing retained. The census says that stance blocks 1358
// of 2143 parser-accepted modules — the `func` bucket is 63% of the corpus, and no other field is
// within a factor of five of it. So this is where the parser starts *retaining*.
//
// **Measured after the fact: 196 → 926 encodable, a gain of 730.** That is the census's whole point
// landing, and the two figures beside it are what the section actually bought — 712 of the 926 now
// carry at least one instruction where the number before was **zero**, and 196 carry no function
// section at all, which is *exactly* the baseline's 196. A section that added function bodies while
// leaving the count of body-less modules unmoved is the consistency check passing: nothing in the
// old set was reclassified, the new set is additive. See encode.go's witness paragraph for the
// independent-producer half — 878 of the 926 join wabt's corpus and agree byte for byte.
//
// The retention is deliberately shallow. An instruction becomes `instr`: an opcode plus its already
// encoded immediate bytes, appended to a flat list in emission order. It is **not** 0002's internal
// form and must not grow toward one — 0002's form is the interpreter's, built from
// `binary.DecodeModule`'s output on the path that has a conformance record, and a second
// representation growing out of the *text* path is exactly the option 0011 refused. What goes in
// this list is bytes with an opcode attached, and the moment anything wants to ask a question about
// an instruction rather than write it, the answer is that the question belongs on the other side of
// the decoder.
//
// # Emission order, which is the one place folding is not free
//
// `expr1`'s plain arm is `plaininstr expr_list` (parser.mly:814): the leader is parsed *first* and
// must be emitted *last*, because `(i32.add (i32.const 1) (i32.const 2))` denotes the same sequence
// as `i32.const 1  i32.const 2  i32.add`. So a folded instruction's operands are collected into a
// nested sink and spliced ahead of the leader. Getting this backwards produces a module that
// decodes clean and computes a different answer — accept-direction, invisible to every vector the
// suite has, because the suite's vectors are modules it expects to *work*.
//
// # Two index resolutions, and why they are not one mechanism
//
// `local.get $x` resolves **at the cursor**: `funcSignature` resets `p.ctx.locals` per function and
// binds params before the body is read, so the name is in scope when it is used and a forward
// reference is meaningless. `call $f` resolves **in the deferred phase**, because a function may be
// called before its definition is parsed. That asymmetry is a fact about the two spaces rather than
// a choice here, and it is why the local path writes its bytes immediately while the call path
// appends a placeholder and patches it under `deferOp` — the same slot-plus-thunk shape
// `defineExport` uses (typetable.go), copied rather than re-derived (*lessons are indexed by shape*).
//
// Both are accept-direction: every `unknown local`/`unknown function` vector in the corpus is an
// `assert_invalid` with a numeric index, so a *symbolic* index resolved to the wrong number is
// something no vector reports.

// # Where the sink lives, and why it is not a parameter
//
// The sink is a field on the parser, set by `funcField` and nil everywhere else. The alternative —
// threading a `*instrSink` parameter — reaches `instrList`, `instr1`, `plaininstr`, `immediates`,
// `expr`, `expr1`, `exprList`, `constexpr1`, `offset`, `elemexpr`, `elemexprList`, `block`,
// `foldedBlock` and the memarg/lane readers below them: fourteen signatures changed to carry a value
// that is the same value at all fourteen, which makes every one of them a place to pass the wrong
// sink. The nested-sink case (folded operands) is served by *swapping* the field and restoring it,
// which is the same `labelReset`/`labelRestore` discipline funcField already uses for label scopes
// and is balanced by defer at the one site that swaps.
//
// A nil sink means "recognize, do not retain", which is the state every non-func caller of the
// instruction readers is in: `offset`, `elemexpr` and the global/data initializers all parse
// instructions this file has no section for yet, and they must keep working unchanged. So `emit` is
// a no-op on nil rather than a panic — the retention is additive to a recognizer that already
// passes 4162 vectors, and a panic there would make an unretained path a crash instead of the
// no-change it is.

// instr is one retained instruction: an opcode and its immediate bytes, ready to write.
//
// The immediates are encoded at parse time rather than stored as values, which is the whole reason
// this type is three fields instead of a variant per shape. `i32.const -1` becomes the bytes
// `7f` here, not an `int32` awaiting a writer, so nothing downstream needs to know that an
// `i32.const`'s immediate is signed while a `local.get`'s is not — that knowledge lives at the one
// site that has the token in hand and the width from the mnemonic.
//
// # Why patch exists rather than a deferred byte slice
//
// A `call $f` cannot encode its immediate at parse time, so `imm` is left empty and `patch` records
// the resolution to run in stage 2. The alternative — deferring the whole instruction — would put
// an instruction's *position* in a thunk, and position is the one thing this list exists to fix.
//
// `patch` is the **whole** immediate when it is set: `resolveFuncs` uses it in place of `imm` rather
// than beside it. Within one immediate, position is carried by `immPart` and composed into a single
// `patch` by `finishImm`, so this type stays two fields and one choice.
type instr struct {
	// op is the opcode's byte sequence, prefix included: one byte for `i32.add`, two for a
	// prefixed opcode. Taken from the generated table, never from a literal here.
	op []byte
	// imm is the encoded immediates, in the order the reference's `encode.ml` arm writes them.
	imm []byte
	// patch, when non-nil, computes the immediate in stage 2. Used only by the symbolic arms
	// whose space allows forward references.
	patch func() ([]byte, error)
}

// instrSink collects instructions in emission order.
//
// A slice with a splice operation rather than an `io.Writer`, for the folded case: operands are
// parsed after their leader and emitted before it, so the sink must be able to accept a nested
// sink's contents *ahead* of an instruction it has not appended yet. A writer cannot seek
// backwards, which is the same reason `writer.section` splices a nested buffer.
type instrSink struct {
	instrs []instr
}

// add appends one instruction.
func (s *instrSink) add(in instr) { s.instrs = append(s.instrs, in) }

// splice appends every instruction from another sink, preserving order.
func (s *instrSink) splice(other *instrSink) { s.instrs = append(s.instrs, other.instrs...) }

// emit appends one instruction to the active sink, or does nothing when there is none.
//
// The nil case is the recognizer path and is silent by design — see this file's header. It is
// `p.sink == nil` rather than a flag, so "am I retaining" and "where do I retain to" cannot
// disagree.
func (p *parser) emit(in instr) {
	if p.sink == nil {
		return
	}
	p.sink.add(in)
}

// retaining reports whether instructions are being kept — whether a sink is installed **right now**.
//
// Read by the arms that must *refuse* rather than skip: an immediate shape this file cannot encode
// has to fail the encode while still parsing cleanly for the recognizer, so it asks this instead of
// checking for nil itself at four sites.
//
// **Not the same question as `p.retain`, and grave #144 is the cost of the two being conflated.**
// `p.retain` is the parse's *mode* — this parse is building a module — and it is true for the whole
// parse. This is true only where a sink is installed, which is inside a function body and, for the
// span of one offset expression, inside `retainedOffset`. The two agree at every site that *inherits*
// a sink from an enclosing instruction, which is why they were interchangeable for three sections;
// they come apart at module-field scope, where nothing installs one.
//
// So the rule, stated where both are in view: a reader that **consumes** an installed sink asks this,
// and a reader that **establishes** retention asks `p.retain`. `intoSink` is the second kind and
// asked the first — a function gating on the condition it exists to create, which is silently a no-op
// wherever the condition does not already hold.
func (p *parser) retaining() bool { return p.sink != nil }

// immPart is one component of the immediate under construction: bytes known at the cursor, or a
// thunk that yields them in stage 2. Exactly one of the two fields is set.
//
// # The component list is grave #130's repair, and the defect was the absence of a position
//
// The predecessor was `imm []byte` plus one `immPatch func() ([]byte, error)` covering the *whole*
// immediate, and that pair can hold a deferred component only when it is the instruction's **only**
// component. Four sites therefore carried a refusal — "cannot yet encode a deferred index after
// another immediate" and its three siblings — and the refusals were the honest part; what they
// could not do is let `memory.init $d` defer its data index and still write the defaulted memory
// index after it, because "after it" is not something a whole-immediate thunk can express.
//
// The consequence was not a refusal, it was resolution **at the cursor** for eight of the nine
// index spaces: one pass where the reference has two (`module_fields1`'s closure). `module_fields`
// admits any field order in the source, so `(module (func (data.drop $s)) (data $s …))` is a valid
// module the predecessor rejected while accepting the reverse order — accept-direction, invisible
// to every vector in the corpus, and invisible to a both-orders differential too when the staging
// under test is what both orders share (see docs/laws/controls.md on correlated arms).
//
// With a position per component the deferral generalizes: any component may wait, the ones around
// it keep their places, and the categories that must *not* defer are the ones whose **space is
// reassigned during the parse** — the locals space, and only it. That is a fact about
// `p.ctx.locals` rather than a list of categories, which is why `retainIdxIn` reads `space.kind`.
type immPart struct {
	// b is the bytes, when they were known at the cursor.
	b []byte
	// later computes the bytes in stage 2, when they were not.
	later func() ([]byte, error)
}

// bytes yields the component, running its thunk if it has one.
//
// Called in stage 2 by `finishImm`'s composed patch and by `emitTryTable`, whose catch clauses are
// `immPart`s for the same reason an immediate's components are: a `catch $t` names a tag the module
// may bind later. One accessor rather than the two-field test written at each site, because the
// invariant *exactly one field is set* is this type's own and not its readers'.
func (part immPart) bytes() ([]byte, error) {
	if part.later == nil {
		return part.b, nil
	}
	return part.later()
}

// appendImm adds encoded immediate bytes to the instruction being built, or does nothing when not
// retaining.
func (p *parser) appendImm(b []byte) {
	if p.sink == nil {
		return
	}
	p.immParts = append(p.immParts, immPart{b: b})
}

// deferImm adds an immediate component whose bytes are stage 2's, **in the position it occupies** —
// `appendImm`'s deferred twin, nil-tolerant for the same reason.
//
// Every caller's thunk must build the *whole* component it stands for: a memarg's flags byte depends
// on the resolved memory index, so `retainMemarg` defers flags-index-offset together rather than
// deferring the index alone. Position separates components; it does not separate a component from
// its own bits.
func (p *parser) deferImm(later func() ([]byte, error)) {
	if p.sink == nil {
		return
	}
	p.immParts = append(p.immParts, immPart{later: later})
}

// finishImm turns the accumulated components into one instruction's immediate: flat bytes when every
// component was known at the cursor, or a single patch composing them in order when any component
// defers.
//
// Flattening the all-known case rather than always returning a patch keeps stage 2's work
// proportional to the deferrals rather than to the instruction count, and keeps `patch != nil`
// meaning what `resolveFuncs` reads it as — *this instruction had something to wait for*.
//
// The component list is **copied** into the deferred closure. `plaininstr` restores the enclosing
// instruction's list on the way out, and that list is a different slice header; copying makes the
// independence a property of this function instead of a property of every caller's reset.
func (p *parser) finishImm() (imm []byte, patch func() ([]byte, error)) {
	deferred := false
	for _, part := range p.immParts {
		if part.later != nil {
			deferred = true
			break
		}
	}
	if !deferred {
		var w writer
		for _, part := range p.immParts {
			w.bytes(part.b)
		}
		return w.b, nil
	}
	parts := slices.Clone(p.immParts)
	return nil, func() ([]byte, error) {
		var w writer
		for _, part := range parts {
			b, err := part.bytes()
			if err != nil {
				return nil, err
			}
			w.bytes(b)
		}
		return w.b, nil
	}
}

// intoSink runs a reader with a fresh sink installed and returns what it emitted.
//
// The swap-and-restore is `labelReset`/`labelRestore`'s discipline for the instruction sink, and it
// exists because two productions need an instruction to land *before* text that was read earlier: a
// folded leader (`expr1`) and a block opener, whose blocktype is not known until its body has been
// read (see orderedTypeUse for why the body comes first). A writer cannot seek backwards; a nested
// sink spliced by the caller can.
//
// **One function rather than the swap written at each site.** `expr1`'s plain arm had it inline and
// was the only site; the block family adds four more, and five hand-written swaps are five places to
// forget the restore — *one concept, one trigger* (#82). The restore is unconditional rather than
// deferred so the sink's value can be returned, which is what the caller splices.
//
// Not retaining is not a special case that skips the read: the reader still runs, because recognizing
// is what the nil-sink path exists to do. It returns an empty sink, which `emitSink` then drops.
//
// **The condition is the mode, `p.retain`, and it was `p.retaining()` — a function that installs a
// sink asking whether one is already installed (grave #144).** The two questions agree everywhere a
// sink is inherited from an enclosing instruction, which was every caller until section 9: five sites
// inside a function body, where `funcField` installed the outer sink before any of them ran. At
// module-field scope they diverge, because nothing installs a sink there — `retainedOffset` installs
// one for the offset alone and restores it before the element list is read — so `elemIdxSink` and
// `elemexprRetained` took the short-circuit and returned empty sinks on a parse that was retaining.
// The visible symptom was `01 01 00 00` for `(module (func) (elem func 0))`: correct flag, correct
// elemkind, and a vector of zero. The table sugar's `min = max = len(einit)` made it unmistakable by
// writing `{Min:0 Max:0}` for a two-element segment.
//
// `retainedOffset` gates on the mode and has since it was `dataOffset`, three functions away and for
// this reason — so this is *lessons are indexed by shape* (#105) with the sibling's version sitting
// in the same package: the mode-versus-sink distinction had already been met and solved, and asking
// `retaining()` here re-derived it wrong. The rule says read the sibling before writing the reader,
// and the sibling is `retainedOffset`.
//
// Falsified as TestIntoSinkGatesOnTheModeNotTheSink, and **the two gates fail differently, which is
// why that control asserts bytes rather than counts.** Reverting this one yields an empty sink *per
// element*, so a segment keeps the right number of elements and each is a bare `0x0b` terminator;
// reverting `elemIdxList`'s yields a nil slice and no elements at all. A count-only assertion sees
// the second and is green for the first — measured, on ten rows. Either way the flags, offsets and
// reftypes stay correct, so the image is legal and denotes a different module: the accept-direction
// shape no `assert_malformed` can see.
func (p *parser) intoSink(read func() error) (instrSink, error) {
	if !p.retain {
		return instrSink{}, read()
	}
	var nested instrSink
	outer := p.sink
	p.sink = &nested
	err := read()
	p.sink = outer
	return nested, err
}

// emitSink appends a nested sink's instructions to the active one, or does nothing when there is
// none — `emit`'s bulk twin, nil-tolerant for the same reason.
func (p *parser) emitSink(s *instrSink) {
	if p.sink == nil {
		return
	}
	p.sink.splice(s)
}

// blockArms is a block-family body split where its *encoding* needs a delimiter, rather than where
// the grammar puts a paren.
//
// Two fields because `If` is the one arm the binary format writes with an internal opcode: `op 0x04;
// blocktype; list instr es1; if es2 <> [] then op 0x05; list instr es2; end_ ()` (encode.ml:252-256).
// `block`, `loop` and `try_table` fill `body` alone and leave `els` empty, which is exactly the state
// that suppresses the ELSE byte — so the three-arm case and the else-less `if` need no special
// handling, they are the same state.
//
// **`els` being empty is the reference's condition, and it is not "was `else` written".** `if …
// else end` writes the keyword and an empty `instr_list`, so `es2` is `[]` and no `0x05` is emitted;
// an encoder keying on the keyword would emit a byte the reference does not. The two spellings
// encode identically, which is the reference's behaviour and not a simplification here.
type blockArms struct {
	// body is the whole body for block/loop/try_table, or the then-arm for `if`.
	body instrSink
	// els is the `if`'s else-arm, empty when there is none or when it is written and empty.
	els instrSink
}

// blockTail wraps a block body reader so its instructions land in an arm instead of in the enclosing
// sequence.
//
// The tail is what `orderedTypeUse` calls before it records the signature, and the body must not be
// emitted where it is read: the opener precedes it in the image and its blocktype immediate is not
// known until the signature has been recorded. So the body is collected and the caller splices.
//
// `if`'s folded arm does **not** go through this — see ifBody, whose operands belong to the
// *enclosing* sequence rather than to either arm.
func (p *parser) blockTail(arms *blockArms, read func() error) func() error {
	return func() error {
		var err error
		arms.body, err = p.intoSink(read)
		return err
	}
}

// emitBlock writes one block-family instruction: the opener with its blocktype, the arms, and END
// (encode.ml:250-256).
//
// **The END is this encoder's to add, exactly as `writeCodeSection`'s per-body terminator is.**
// `end_ ()` closes every arm of the family, and the text spells it for the flat forms only — a folded
// `(block)` has no `end` token at all. Both spellings encode the same byte, so it is emitted here for
// both rather than at the site that happened to read a keyword.
//
// The opener's opcode comes from the generated table under the keyword's own text, so this function
// states no opcode of its own except the two delimiters — which have no mnemonic to look up (see
// opElse). `block`, `loop` and `if` are all unambiguous rows, so the lookup cannot fail for the three
// keywords that reach here; the refusal is written anyway, because "cannot fail" is a claim about
// today's table and `opBytes`' other callers all state it the same way.
func (p *parser) emitBlock(kw Token, slot *blockTypeSlot, ft funcType, arms *blockArms) error {
	if !p.retaining() {
		return nil
	}
	op, ok := opBytes(kw.Text)
	if !ok {
		return errf(kw, "cannot yet encode the %s instruction (#8)", kw.Text)
	}
	p.emit(instr{op: op, patch: p.blockTypeBytes(slot, ft, kw)})
	p.emitSink(&arms.body)
	if len(arms.els.instrs) > 0 {
		p.emit(instr{op: []byte{opElse}})
		p.emitSink(&arms.els)
	}
	p.emit(instr{op: []byte{opEnd}})
	return nil
}

// emitTryTable writes `try_table`: the opener with its blocktype and catch vector, the body, and
// END (decode.ml:412-417, encode.ml:257-259: `op 0x1f; blocktype bt; vec catch cs; list instr es;
// end_ ()`).
//
// **`emitBlock`'s structure exactly, with one extra immediate component.** try_table has no
// `els` arm (`handlerClauses`' body always lands in `arms.body`, never `arms.els` — there is no
// `else` in this family), so this omits that half of emitBlock rather than taking an unused
// parameter. `hv` is the clause components `handlerClauses` built — see the `handlerVec` field's
// comment for why a clause is an `immPart` rather than a `[]byte` — and the vector's count is
// `len(hv)`, known now because every clause has already been read. The count is knowable at the
// cursor even where a clause's *bytes* are not, which is what lets a deferred `catch $t` sit inside
// a `vec` whose length is already fixed.
//
// **`hv` and the blocktype are one patched immediate, not two `emit` calls**, because `emit`
// writes one opcode per call and try_table's opcode is written once: `p.blockTypeBytes`'s thunk
// and the catch vector's bytes are concatenated inside one patch function.
func (p *parser) emitTryTable(kw Token, slot *blockTypeSlot, ft funcType, hv []immPart, arms *blockArms) error {
	if !p.retaining() {
		return nil
	}
	op, ok := opBytes(kw.Text)
	if !ok {
		// Unreachable today — try_table has exactly one row in the generated table — kept for the
		// same reason emitBlock keeps its own unreachable check: "cannot fail" is a claim about the
		// table as it stands, not a proof, and every other opBytes caller states it the same way.
		return errf(kw, "cannot yet encode the %s instruction (#8)", kw.Text)
	}
	btPatch := p.blockTypeBytes(slot, ft, kw)
	p.emit(instr{op: op, patch: func() ([]byte, error) {
		bt, err := btPatch()
		if err != nil {
			return nil, err
		}
		// Every clause is resolved before the vector is written, because `writer.vec`'s element
		// callback cannot fail and a clause's thunk can — the same reason `resolveFuncs` runs the
		// instruction patches into a value rather than inside a section writer.
		clauses := make([][]byte, len(hv))
		for i, part := range hv {
			b, cerr := part.bytes()
			if cerr != nil {
				return nil, cerr
			}
			clauses[i] = b
		}
		var w writer
		w.bytes(bt)
		w.vec(len(clauses), func(w *writer, i int) { w.bytes(clauses[i]) })
		return w.b, nil
	}})
	p.emitSink(&arms.body)
	p.emit(instr{op: []byte{opEnd}})
	return nil
}

// refuseUnencodable is the instruction frontier: it errors when retaining and does nothing
// otherwise.
//
// **The whole reason this exists is that an instruction which parses and emits nothing is worse than
// one that fails.** `blockinstr` reads a `block … end` perfectly well and this file has no opcode for
// it; if retention simply skipped it, the body's *inner* instructions would still be emitted and the
// image would hold a function whose control flow is gone — a module that decodes clean, validates,
// and computes something else. That is the accept-direction class in its purest form, and the suite
// scores it green by construction because every vector containing a block is a module the suite
// expects to work.
//
// So the shape is: parse everything, emit what is encodable, and **refuse the module** the moment
// something unencodable is retained. `ReadModule` never retains, so all 4162 vectors are untouched
// by every call site of this function — which is the property that lets the frontier be drawn
// narrowly and moved outward one tier at a time.
//
// The message names the construct and this issue, never a spec string: reporting malformedness for
// well-formed text would lie about the module to conceal the encoder's own frontier (#5's ruling).
func (p *parser) refuseUnencodable(t Token, what string) error {
	if !p.retaining() {
		return nil
	}
	return errf(t, "cannot yet encode %s (#8)", what)
}

// encodableShapes are the immediate shapes the code section can write.
//
// **Five of sixteen, and the boundary is a measurement rather than a preference.** The census
// partitions the 1358 modules the `func` bucket blocks by their hardest instruction: 697 need only
// these five, 68 more need `memarg`, 87 more need control flow, and 403 are behind a gate this
// build does not turn on. So this set is the largest one whose emission is a lookup — no natural
// alignment defaults, no block types, no operand-dependent opcodes — and each of the other three
// tiers is its own question with its own authority to read.
//
// **The prediction was 697 and the delivered gain is 730, so the tier boundary is not the predictor
// the census took it for** — 33 modules over, and an estimate that is *generous* still needs its
// error bounded, because a tier count nobody re-measured is the next tier's estimate too. The excess
// is accounted for and then some: **82 of the newly-encodable modules use a prefixed opcode**, across
// 54 files led by `memory_copy`/`memory_copy64` at 10 each and the SIMD arithmetic files at 1–2. The
// census counted those in its "behind a gate" tier, and they encode here because **the text front end
// reads no `Features` at all** — the whole package has zero references to it, gating living entirely
// in the decoder. So `EncodeModule` writes `memory.copy` whether or not bulk-memory is on, and the
// census's fourth tier was measuring the *decoder's* gates against the *encoder's* frontier: two
// different questions joined on a name.
//
// **That gate-blindness is designed, and ruled so: it is contraindicated to change, not merely
// premature** (Scott, #124). Gate enforcement belongs to the **sole module authority** — the decoder,
// which every encoded image already passes through at the check-by-decoding step, where a gated
// construct gets its feature-named decline. An encoder reading `Features` would be a *second* gate
// predicate: two authorities for one truth, which is the drift 0011's sole-authority rule exists to
// forbid. So the discrepancy is a **census-lens artifact**, and the repair is to label the lens —
// measure through the decoder's gates, since that is what conformance sees — rather than to arm this
// package. The one thing that may cross the boundary is the caller's `Features` as **pass-through
// configuration** for the decode check; nothing here branches on it, and a `Features` reaching this
// package is not a counter-example to the ruling as long as that stays true.
//
// The planning consequence stands either way: the remaining tier counts (68 memarg, 87 control) are
// **upper-bound-shaped rather than exact**, so whichever PR takes tier 2 re-measures rather than
// quoting them. **Tier 2 re-measured and the pre-registration held**: the 9,529-vector memarg census
// paid 199 modules, because a bucket keyed on a module's *first* refusal moves it only when nothing
// else in the module is unencodable, and the freed `address*.wast`/`memory_copy*.wast` sweeps hit
// `(data …)` on the next pass.
//
// Held as a set rather than as a `switch` in `immediates` so the frontier is one legible list, and
// so the shapes *outside* it refuse structurally: a shape added to `immShape` and not to this map
// is refused rather than silently emitting an instruction with no immediates.
var encodableShapes = map[immShape]bool{
	immNone:      true,
	immIdx:       true,
	immIdxOpt:    true,
	immIdxIdxOpt: true,
	immNum:       true,
	immMemarg:    true, // tier 2 (#8): retainMemarg, over the generated naturalAlign table

	// The seven `STRUCT_GET`/`STRUCT_SET`/array `idx idx` mnemonics and `array.new_fixed`'s `idx
	// nat32` — `idxPairRetained` and `nat32Retained`, both over `retainIdxIn`'s existing category
	// resolution. See `idxPairRetained`'s comment for why the two field-space mnemonics need no
	// separate entry: a numeric field index resolves without ever consulting the (absent)
	// per-struct-type space, and a symbolic one is refused there, not here.
	immIdxIdx:   true,
	immIdxNat32: true,

	// `br_table`, whose immediates are the one case in this map that is not a concatenation of
	// what the parser read: see brTable for the count, the split_last, and why the sequence is
	// never empty. Admitted with the interpreter arm rather than with the block family, because an
	// encoder that can write `br_table` into a module nothing can execute buys no vectors.
	immIdxIdxList: true,

	// **`ref.null`, and it is no longer the partial entry it was admitted as.** The paragraph here
	// said this was "the one entry whose shape is only *partly* encodable" — twelve keywords plus a
	// type index (parser.mly:361-374) of which two had an unparameterized encoding — and that was
	// true for the two PRs it stood. `heapTypeBytes` now writes the whole production: twelve
	// `s7` singletons from `absoluteHeaptypeBytes` and the index form as a `typeuse s33`. The
	// remaining `false` is a thirteenth heap type, which is a red board rather than a frontier.
	//
	// **The refusal that stayed at the reader is the *deferral* one, and it is a different fact.**
	// `heaptypeRetained` still reports at the cursor when a symbolic index cannot resolve there,
	// because that is where the token is, and a frontier message with a source position is worth
	// more than one naming a field number. Same division the code section already uses — a func's
	// signature is refused in `encodableOrErr`'s type loop and its body at the cursor.
	//
	// Admitted for #8's global-section work: `(global funcref (ref.null func))` is `global.wast`'s
	// opening line, and the section and this shape each blocked it independently. Widened to the
	// whole production for the board's largest bucket — **609** vectors across six keys, of which
	// 301 were the index form (#8).
	//
	// **Those 609 converted nothing, and the reason is worth keeping here rather than in a PR
	// description.** The board's fail column is *unmoved* — 1699 before and after, encode stratum
	// 994 both — because every one of the 609 re-bucketed into a sibling frontier one layer up:
	// +446 into `needs a parameterized reference encoding` (a table's or global's or type's
	// *reftype*, `valTypeBytes`) and +163 into `ref.test`/`ref.cast`/`br_on_cast` immediates. The
	// bucket was **shadowing**: `ref.null` is the first refusal a GC vector meets, so its key was
	// counting vectors that need three or four other things as well.
	//
	// So the bucket-size-estimates-the-reward rule cuts in its over-promise direction here, and
	// harder than the 18-quoted-4-reachable case that made it: not "some members are blocked on
	// another mechanism" but *all* of them, with the arm nonetheless correct and required. A key
	// naming the **first** refusal in a chain is an upper bound on the reward and says nothing about
	// the rest of the chain — partitioning by mechanism cannot see that, because the partition is
	// over the failures the board can *see*, and the second refusal is invisible until the first is
	// gone. What would have seen it: asking, before starting, what the vectors in the bucket *are*.
	// `br_table.wast` 146, `ref_eq.wast` 82, `ref_test.wast` 68, `i31.wast` 61 — GC files throughout,
	// where a `ref.null` is one token in a module that also declares `(ref null $t)` fields.
	//
	// This is now a *rule* rather than a lesson at one site: the census rule's third clause, and the
	// **co-blocking probe runs before a bucket is selected** — bucket size × sole-blocker fraction =
	// expected pay. Written here as the specimen the clause was measured on, not as the statement of
	// it (ruling: Scott, on this arm's board).
	immHeaptype: true,

	// **`ref.test`/`ref.cast`'s reftype operand and `br_on_cast`/`br_on_cast_fail`'s pair of
	// them** — the +163 the paragraph above names, landing behind `reftypeRetained` and
	// `brOnCastRetained` respectively (#8). Both delegate their heaptype halves to
	// `heaptypeBytesOf`, so the same forward-reference deferral and the same thirteenth-heap-type
	// frontier `immHeaptype`'s row states apply here unchanged; what these two shapes add is the
	// nullability-to-opcode/flags-byte translation their own doc comments carry.
	immReftype:     true,
	immIdxReftype2: true,

	// **The four remaining shapes, and their landing closes the immediate-shape frontier
	// entirely — #210, the last four `false`s in this file.** `v128.const` (`immVecConst`,
	// `vecConst`'s own per-lane `laneBytes`), `i8x16.shuffle` (`immLaneIdxList`, sixteen raw
	// bytes via `laneidx`'s own retention), `extract_lane`/`replace_lane` (`immLaneIdx`, one raw
	// byte, same `laneidx`), and the eight `load*_lane`/`store*_lane` mnemonics (`immLaneImms`,
	// the existing memarg encoding plus one trailing raw byte, `laneImms`). All four were parsed
	// and range-checked in full before this PR — only the byte writers were missing, the same
	// shape retention has taken at every other tier of #8.
	immVecConst:    true,
	immLaneIdxList: true,
	immLaneIdx:     true,
	immLaneImms:    true,
}

// reservedByteWireForms are the mnemonics whose **wire form carries a byte no immediate shape
// writes**, and they are refused for that reason rather than for their shape's.
//
// `encodableShapes` above is keyed on the immediate *shape*, which is the right key for every
// frontier #8 has taken: a shape is the reference's written immediate sequence, and a sequence this
// file cannot write is one no mnemonic carrying it can encode. This map is the case that key cannot
// express. `atomic.fence`'s `plaininstr` arm is empty — `| ATOMIC_FENCE { fun c -> atomic_fence }`,
// spec-threads/parser.mly:455 — so its shape is `immNone`, which *is* encodable and correctly so for
// the twenty-four other kinds carrying it. Its encoding is three bytes:
//
//	| AtomicFence ->
//	  op 0xfe; op 0x03; op 0x00
//
// spec-threads/encode.ml:305-306, mirrored on the decode side by `expect 0x00 s "zero flag expected"`
// (spec-threads/decode.ml:786) and already held as `imms: []imm{immZeroByte}` in
// internal/binary/optable.go. The third byte belongs to the *encoding*, not to the grammar, so
// `immediates` has nothing to accumulate for it and `plaininstr` would emit `fe 03` and stop —
// a truncated instruction, emitted while reporting success. Accept direction, and no
// `assert_malformed` can see it.
//
// **No fetched-corpus vector reaches this, and the reachability is stated because the first draft of
// this comment got it wrong.** That draft cited `atomic.wast:965` for a module expecting the fence to
// work. The line exists — in `third_party/spec-threads/test/core/threads/atomic.wast`, the *pinned
// snapshot's* copy (1018 lines, with `(func (export "fence") (atomic.fence))` and its
// `assert_return`). The file the threads lane actually walks is
// `testdata/spec/proposals/threads/atomic.wast` at suite pin de54fd2 — 539 lines, and `atomic.fence`
// appears nowhere in the fetched suite at all. Two paths, one basename, and the grep answered from
// the stale one: 264 of the corpus's 288 `.wast` basenames also exist under `third_party/`, both
// trees gitignored, so this is a standing hazard rather than one slip (#533).
//
// So the corpus cannot witness this refusal, which is an argument *for* the refusal and against
// trusting the board about it: `TestAtomicEncodeReachesTheFrontierAndStops`' hand-built row is the
// only thing that fires it, and a truncation nothing asks about is exactly the accept-direction
// defect that ships. The grammar pin and the corpus pin disagree about the population — cc535ad has
// the arm, de54fd2 has no vector — and the reader is built to the grammar.
//
// **Keyed by mnemonic, and the key is the honest one here**: the fact is about one instruction's wire
// form, not about a class. It is one arm of the sixty-seven in the 0xfe region, and *which* one is
// derived from the region rather than trusted — the arms that write a byte beyond the two-byte opcode
// without calling `memop` are exactly this map's members
// (TestReservedByteWireFormsAreTheReferences).
//
// #532 holds the two ways to close it; both put the reserved byte somewhere, and choosing between
// them is a decision about whether `immShape` names grammar or encoding. Out of #524's second half by
// its pre-registration — that slice stops at the reader.
var reservedByteWireForms = map[string]bool{
	"atomic.fence": true, // fe 03 00 — spec-threads/encode.ml:305
}

// heaptypeRetained reads `ref.null`'s heap type immediate and encodes it.
//
// **The immediate is a bare `heaptype`, not a `reftype`, and the twelve-form agreement between them is
// a trap rather than a convenience.** `RefNull t -> op 0xd0; heaptype t` (encode.ml:414): a heaptype
// has no nullability field, because the *instruction* is the null. The two productions write the
// identical byte for all twelve absolute forms — `reftype`'s abbreviation arms *are* `heaptype`'s
// singletons — and diverge on `null` and on the index form, where `reftype` prefixes `-0x1d`/`-0x1c`
// before recursing (encode.ml:137-151). So a reader that used `reftype` here would be right on every
// module whose heap type is absolute and wrong on the first `(ref.null $t)`, which is the shape that
// gets found late.
//
// It also governs the *diagnostic*: a refusal renders through `heapString`, not `String`, because a
// heap type quoted in reftype spelling claims a nullability the source never wrote. That is grave
// #36's invented-evidence class in the half of a message no expected string reads, and it is the
// reason `heapString` exists as a second method rather than a flag on the first.
//
// # The symbolic index defers, and that is the type space's own design rather than a widening
//
// The paragraph here used to say resolution is "at the cursor rather than deferred", on the ground
// that "a type index heaptype is *refused* either way, so there is nothing a deferral could buy".
// The premise expired when `heapTypeBytes` grew the index form, and the conclusion has to go with it:
// with the form encodable, resolving `$t` at the cursor rejects
// `(module (func (drop (ref.null $t))) (type $t (func)))` — a **valid** module, measured as rejected
// with `unknown type $t` before this change.
//
// It is grave #130's class, and this is the one category where the deferral is not an extension of
// scope but the documented design: the type space *permits forward references by construction*
// (mutually recursive types), which is why `heaptype` returns its token unresolved at all and why
// `typetable.go`'s whole deferred phase exists. Resolving here would be the defect that file's header
// warns about. The corpus does not distinguish the two — a sweep of all 254 `.wast` files finds
// **zero** `ref.null $t` preceding its `(type $t …)`, so this is accept-direction and invisible
// (§9 G-3), and `TestRefNullHeaptypeResolvesInBothFieldOrders` is a derived vector saying so.
//
// A **numeric** index needs no deferral — `resolveTypeIdx` returns it unchanged, the reference's `idx`
// NAT arm being `nat32 $1` with no lookup — so it takes the cursor path with the absolute forms, and
// the split is by *arm* rather than by category.
func (p *parser) heaptypeRetained(mnemonic Token) error {
	tok := p.c.peek()
	h, err := p.heaptype()
	if err != nil {
		return err
	}
	if !p.retaining() {
		return nil
	}
	return p.retainHeapTypeImm(mnemonic, h, tok)
}

// retainHeapTypeImm appends one heap type as an immediate component, deferring it when the only
// spelling that cannot resolve at the cursor is the one written.
//
// **The three callers were three copies of this five-line shape**, each with its own pair of guards
// asserting that no other immediate was present — because the predecessor's whole-immediate thunk
// had no way to sit beside one. With `immPart` carrying position those guards are gone, and what is
// left is small enough that three copies is the drift risk `isForwardHeapRef`'s own comment names.
// So the deferral rule for heap types lives at one site: `ref.null`'s, `ref.test`/`ref.cast`'s, and
// **each** of `br_on_cast`'s two, which may independently be forward references.
func (p *parser) retainHeapTypeImm(mnemonic Token, h heapRef, tok Token) error {
	if isForwardHeapRef(h) {
		p.deferImm(func() ([]byte, error) {
			return p.heaptypeBytesOf(mnemonic, h, tok)
		})
		return nil
	}
	b, err := p.heaptypeBytesOf(mnemonic, h, tok)
	if err != nil {
		return err
	}
	p.appendImm(b)
	return nil
}

// heaptypeBytesOf resolves and encodes one heap type, at whichever stage its caller runs in.
//
// One function for both timings, so the *encoding* cannot differ between the deferred arm and the
// cursor arm — two copies of a resolve-then-encode pair is the two-places-know-one-fact shape, and
// here the fact is which bytes a heap type is.
func (p *parser) heaptypeBytesOf(mnemonic Token, h heapRef, tok Token) ([]byte, error) {
	rv, err := p.ctx.resolveVal(valType{heap: h})
	if err != nil {
		// A type index naming nothing: the module is ill-formed and the resolver's message says so
		// with the token, so it is returned rather than converted into a frontier report.
		return nil, err
	}
	b, ok := heapTypeBytes(rv)
	if !ok {
		// A thirteenth heap type, added to `absoluteHeaptypes` without a byte — see
		// `heapTypeBytes`. Reported as a frontier rather than panicked, because the honest verdict
		// for a form this encoder has no byte for is a declined module, and
		// `TestEveryAbsoluteHeaptypeHasAByte` is what keeps this arm from being how the omission is
		// discovered.
		return nil, errf(tok, "cannot yet encode the heap type %s as %s's immediate (#8)",
			rv.heapString(), mnemonic.Text)
	}
	return b, nil
}

// reftypeRetained parses one `reftype` operand — nullability and heap type together — and retains
// its **heaptype half** as the wire immediate, its **nullability half** as the opcode choice.
// Used by `REF_CAST`/`REF_TEST`'s `immReftype` shape.
//
// **The wire immediate is a bare `heaptype`, exactly `ref.null`'s, and that is `optable.go`'s own
// authority, not a re-derivation**: `0x14: {mnemonic: "ref_test", imms: []imm{immHeapType}, …}`
// (internal/binary/optable.go) — one `immHeapType`, the same tag `ref.null`'s row carries — and
// `encode.ml:421-424` writes `heaptype t` after the opcode, never `reftype t`. Wasm's `reftype` is
// `(nullability, heaptype)`, but `ref_test`/`ref_cast`'s constructors take that pair *split*: the
// nullability selects which of two opcodes to write (`RefTest (NoNull, t)` is `0x14`, `RefTest
// (Null, t)` is `0x15`), and only the heaptype `t` is bytes in the stream. So this delegates the
// heaptype half to `heaptypeBytesOf` — the exact function `ref.null`'s `heaptypeRetained` already
// uses, both callers resolving through the one `p.ctx.resolveVal`/`heapTypeBytes` pair — and never
// reaches for `valTypeBytes`, which would write a nullability *byte* the wire form has no room for.
//
// **The forward-referencing arm mirrors `heaptypeRetained`'s** (*lessons are indexed by shape*,
// #105): copied rather than re-derived, including both guards, structural here for the same
// reason — `ref.test`/`ref.cast` each have exactly one immediate.
//
// **The opcode choice is set ahead of the deferral branch, not inside either of its arms.**
// `refCastOpBytes` chooses on nullability alone, and `p.reftype()`'s `null` field is known at the
// cursor whether the *heap type* it pairs with is a bound name, an unbound forward reference, or
// an abstract keyword — `null_opt` (parser.mly:357-359) is read and decided before `heaptype` is
// ever called. Setting it once here, rather than duplicating the call in both arms below, is the
// same "one fact, one site" reasoning `heaptypeBytesOf`'s own doc gives for sharing an encoder
// across timings.
func (p *parser) reftypeRetained(mnemonic Token) error {
	tok := p.c.peek()
	v, err := p.reftype()
	if err != nil {
		return err
	}
	if !p.retaining() {
		return nil
	}
	op, ok := refCastOpBytes(mnemonic, v.null)
	if !ok {
		// Unreachable: reftypeRetained's only caller (immediates' immReftype arm) passes exactly
		// REF_CAST/REF_TEST, which are exactly what refCastOpBytes recognizes. A panic rather than
		// a silent fallback, because opBytes' own ambiguousOpcodes refusal is what this function
		// exists to resolve — reaching here unresolved would fall through to that refusal wearing
		// this function's confidence instead.
		panic("text: reftypeRetained called for a mnemonic refCastOpBytes does not recognize: " +
			mnemonic.Text)
	}
	p.opOverride = op
	return p.retainHeapTypeImm(mnemonic, v.heap, tok)
}

// isForwardHeapRef reports whether a parsed heap type is the one shape that cannot resolve at
// the cursor — a symbolic name, per `heaptypeRetained`'s own guard.
//
// Factored out because `brOnCastRetained` below asks it of *two* heap types independently,
// where `heaptypeRetained`/`reftypeRetained` each ask it of one; a second inline copy of the
// same two-field test would be the kind of small duplication `idxPairLookupKinds`' comment warns
// drifts silently.
func isForwardHeapRef(h heapRef) bool {
	return h.abs == "" && h.tok.Kind == VarTok
}

// brOnCastRetained parses `BR_ON_CAST`/`BR_ON_CAST_FAIL`'s `idx reftype reftype` immediates — a
// label then two casts' worth of reftype — and retains them as the wire form's `byte idx heaptype
// heaptype`, per `decode.ml:640-646`/`encode.ml:266-271`:
//
//	let flags = byte s in
//	require (flags land 0xfc = 0) s (pos + 2) "malformed br_on_cast flags";
//	let x = at idx s in
//	let rt1 = ((if bit 0 flags then Null else NoNull), heaptype s) in
//	let rt2 = ((if bit 1 flags then Null else NoNull), heaptype s) in
//
//	let flags = bit 0 (nul1 = Null) + bit 1 (nul2 = Null) in
//	op 0xfb; op 0x18 (or 0x19); byte flags; idx x; heaptype t1; heaptype t2
//
// **The wire order is not the parse order, and that is this function's whole reason to exist
// rather than three calls threaded through the label-taking switch.** The text is `idx reftype
// reftype` (parser.mly:567) and the image is `flags idx heaptype heaptype` — the flags byte,
// encoding *both* reftypes' nullability, precedes the label index that the text writes first.
// `retainMemarg`'s doc states the identical shape for `load`/`store` (text writes the memory
// index before the flags the image writes first) and is the precedent copied here rather than
// re-derived: parse everything into values, then write the image's byte order once every value
// is in hand. A reader that retained the label index as it read it (`labelIdx`'s ordinary path)
// would write that index *before* a flags byte this production has not computed yet.
//
// **Each reftype's wire form is its bare heaptype, not a full parameterized reftype byte** — the
// same fact `reftypeRetained`'s doc states for `ref.test`/`ref.cast`, and for the identical
// reason: `optable.go`'s row for 0x18/0x19 is `imms: []imm{immByte, immIdx, immHeapType,
// immHeapType}`, two `immHeapType` tags, and `encode.ml` writes `heaptype t1; heaptype t2` — never
// `reftype`. So both reftypes' *heap* halves go through `heaptypeBytesOf`, ref.null's own encoder,
// and their *nullability* halves are folded into the one flags byte instead of two per-type bits.
//
// **The label resolves at the cursor and never defers; either heap type may, independently.** Labels
// are lexical (labelIdx's own comment), so `labelIdxValue` runs immediately and its result is a plain
// `uint32` with nothing to patch. A heap type naming a forward-referenced type index is the mutually
// recursive types case `retainHeapTypeImm` defers for, and either of `br_on_cast`'s two reftypes can
// independently be that case — `br_on_cast $l (ref $future1) (ref $future2) …` names two types the
// grammar admits in any order relative to this instruction.
//
// **So this writes four components rather than one patch**, which is what changed with grave #130's
// repair. The predecessor rebuilt the *whole* immediate — flags, the already-resolved label, and both
// heaptypes — inside one thunk whenever either heap type deferred, because `resolveFuncs` replaced
// `in.imm` wholesale and a patch that deferred one heaptype would have dropped the label and flags
// that preceded it. `immPart` carries position, so each part goes where it belongs: the flags byte and
// the label are known here and appended, and each heap type appends or defers on its own. The eager
// half is not incidental — an unencodable heap type beside a deferred one now reports at the cursor
// instead of in stage 2.
func (p *parser) brOnCastRetained(mnemonic Token) error {
	depth, err := p.labelIdxValue()
	if err != nil {
		return err
	}
	tok1 := p.c.peek()
	rt1, err := p.reftype()
	if err != nil {
		return err
	}
	tok2 := p.c.peek()
	rt2, err := p.reftype()
	if err != nil {
		return err
	}
	if !p.retaining() {
		return nil
	}
	var flags byte
	if rt1.null {
		flags |= 0x01
	}
	if rt2.null {
		flags |= 0x02
	}
	var w writer
	w.byte1(flags)
	w.bytes(encodeLocalIdx(depth))
	p.appendImm(w.b)
	if err := p.retainHeapTypeImm(mnemonic, rt1.heap, tok1); err != nil {
		return err
	}
	return p.retainHeapTypeImm(mnemonic, rt2.heap, tok2)
}

// idxRetained parses one index immediate and retains it, resolving in the category the mnemonic's
// arm names.
//
// This is the wrapper `immediates`' `immIdx` arm calls in place of `idx`, and the split from `idx`
// is deliberate: `idx` is still the reader for every *module field* position, where no instruction
// is being built and no category applies.
func (p *parser) idxRetained(mnemonic Token) error {
	r, err := p.idxValue()
	if err != nil {
		return err
	}
	return p.retainIdx(mnemonic, r)
}

// retainIdx encodes one already-parsed index reference as the current instruction's immediate.
//
// **The two resolution timings live here, and which one applies is a property of the space rather
// than of this call.** A numeric index needs no resolution at all. A symbolic index in a space the
// parse **reassigns** — locals, replaced per function — must resolve at the cursor, because a
// deferred lookup would meet the last function's space. Every other space is filled by module fields
// and never reassigned, so a symbolic index in one defers: `module_fields` admits any field order,
// which makes a use-before-definition legal text in all eight of them and not just in the func space
// where `call $f` made it unmissable (grave #130).
//
// Resolving the reassigned space at the cursor is not an optimization. `p.ctx.locals` is replaced per
// function, so a local resolution deferred to stage 2 would run against whichever function's locals
// happened to be current at the end of the parse, which is the last one. That is an
// accept-direction defect and would score green on every vector in the corpus.
func (p *parser) retainIdx(mnemonic Token, r idxRef) error {
	return p.retainIdxIn(mnemonic, r, idxLookupKinds[mnemonic.Keyword])
}

// retainIdxIn is retainIdx with the category given rather than looked up.
//
// The split exists because a **two-index** arm's indices resolve in two *different* spaces —
// `table.init $t $e` is a table then an element segment — so the category cannot be a property of
// the mnemonic there. `idxLookupKinds` holds only the first, and deliberately: see
// `idxPairLookupKinds`, which holds both and disagrees with it about the same kind for a reason.
func (p *parser) retainIdxIn(mnemonic Token, r idxRef, cat idxCategory) error {
	if !p.retaining() {
		return nil
	}
	if !r.isVar {
		p.appendImm(encodeLocalIdx(r.idx))
		return nil
	}
	space := p.idxSpaceFor(cat)
	if space == nil {
		return errf(r.tok, "cannot yet encode a symbolic index on %s (#8)", mnemonic.Text)
	}
	// **The locals space is short here, and this is the one place that can tell.** A typeuse with no
	// re-stated signature contributes its referenced type's params as anonymous locals, so the space
	// holds only the *declared* locals and a symbolic name resolves one slot per param too low. The
	// ordinal is read now, against this function's space; the param count arrives in stage 2 through
	// `localsParamOffset`, and the two are added where the immediate is finally written (#77).
	//
	// **A numeric index is untouched, and that asymmetry is the whole shape of the fix.** `local.get 0`
	// means slot 0 whatever the space holds — it is absolute, and the arm above has already returned.
	// What needed the offset was never the typeuse, it was the *resolution of a name* against a space
	// the typeuse left incomplete.
	//
	// The predecessor refused instead, and it also over-refused: a typeuse naming a param-less type
	// (`(type $t (func))`) binds nothing, so `$v` really was slot 0, and it was declined anyway because
	// "does it have params" was as unanswerable at the cursor as "how many". Both costs go together,
	// because a thunk that reports 0 needs no special case for the type that has no params.
	if space.kind == spaceLocal {
		// **The one space that must resolve at the cursor, and the reason is that it is reassigned.**
		// `funcField` replaces `p.ctx.locals` per function (parser.go's `space{kind: spaceLocal}`
		// assignment), so a *name* looked up in stage 2 would meet whichever function's space was
		// installed last — the last one — which is an accept-direction defect that would score green
		// on every vector in the corpus. Read as `space.kind` rather than as `cat == catLocal` so the
		// condition names the property it depends on: reassignment, not identity.
		ord, err := space.resolveSpaceIdx(r)
		if err != nil {
			return err
		}
		if p.localsParamOffset == nil {
			p.appendImm(encodeLocalIdx(ord))
			return nil
		}
		// A typeuse supplies this function's params, so the space holds only the *declared* locals
		// and the absolute index is the ordinal plus a param count that is not knowable here — the
		// type may be defined later in the field list (#77). The name resolves now and the offset
		// alone defers, which is what keeps the reassignment above harmless.
		off := p.localsParamOffset
		p.deferImm(func() ([]byte, error) {
			n, err := off()
			if err != nil {
				return nil, err
			}
			return encodeLocalIdx(ord + n), nil
		})
		return nil
	}
	// **Every other space defers, and that is grave #130's repair.** These eight are filled by
	// module fields and never reassigned, so `module_fields`' free field order means a name here can
	// legitimately be bound *after* the instruction that uses it: `(module (func (data.drop $s))
	// (data $s …))` is a valid module. Resolving at the cursor rejected it while accepting the
	// reverse order — one pass where the reference has two (`module_fields1`'s second closure, which
	// this now is for every category rather than for `catFunc` alone).
	//
	// The space *pointer* is captured, which is sound for exactly these eight and unsound for the
	// one above: `&p.ctx.funcs` stays valid because the field is never reassigned, and `p.ctx` itself
	// is set once at construction (`encode.go`'s `&parser{…, ctx: newContext(), …}`). The same
	// capture over `locals` would hold a space the parse has already discarded.
	p.deferImm(func() ([]byte, error) {
		idx, err := space.resolveSpaceIdx(r)
		if err != nil {
			return nil, err
		}
		return encodeLocalIdx(idx), nil
	})
	return nil
}

// retainIdxPair encodes the one-or-two index immediates of an `idx_idx_opt` or `init`-sugar arm.
//
// # Written order is not wire order, and only for two of the four mnemonics
//
// `encode.ml` writes the `init` pair **reversed** and the `copy` pair in order:
//
//	TableInit  (x, y) -> op 0xfc; u32 0x0cl; idx y; idx x   (:294)
//	MemoryInit (x, y) -> op 0xfc; u32 0x08l; idx y; idx x   (:411)
//	TableCopy  (x, y) -> op 0xfc; u32 0x0el; idx x; idx y   (:293)
//	MemoryCopy (x, y) -> op 0xfc; u32 0x0al; idx x; idx y   (:410)
//
// and `decode.ml` reads them back the same way — `0x0cl -> let y = at idx s in let x = at idx s in
// table_init x y` (:674) against `0x0el -> let x = … let y = …` (:676). So for `table.init` and
// `memory.init` the *segment* index is written second and encoded first. The reversal is not a
// property of the shape: two mnemonics sharing this function reverse and two do not, which is why
// the order is keyed by mnemonic below rather than assumed from the arm.
//
// # The sugar arm defaults the index that is encoded second
//
// `TABLE_INIT idx` is `table_init (0l @@ $loc($1)) ($2 c elem)` (parser.mly:589) — the one written
// index is the **elem**, and the table defaults to 0. `MEMORY_INIT idx` is the same with `data`
// (:609). Composed with the reversal, the sugar arm's single written index is therefore the
// **first** immediate on the wire and the defaulted 0 is the second: two reversals that cancel for
// the two-index spelling and do not cancel for the sugar one. Getting that backwards emits
// `(table.init 3)` as "segment 0 into table 3" — a well-formed image denoting a different
// instruction, and one no suite vector can report (§9 G-3), since both spellings decode cleanly.
//
// `idx_idx_opt`'s own empty arm (`memory.copy` with nothing written) is handled by the caller, which
// writes two explicit zeroes; there is no one-index spelling of the copy forms at all.
//
// # Each index resolves in its own space
//
// `table.init $t $e` is a table then an element segment (`table_init ($2 c table) ($3 c elem)`,
// :587-588), so a single mnemonic-wide category is wrong here in a way it is not for `table.copy`
// (whose `idx_idx_opt` passes one lookup to both). `pairCategories` holds the pair; the refusal
// this function used to open with — "both the argument reversal and the sugar default apply, and
// neither is exercised by the slice" — was the honest answer while neither table existed.
func (p *parser) retainIdxPair(mnemonic Token, first, second idxRef, havePair bool) error {
	if !p.retaining() {
		return nil
	}
	cats := pairCategories(mnemonic.Keyword)
	if !havePair {
		if !initSugarKinds[mnemonic.Keyword] {
			// `memory.copy 0` — one index written where the encoding wants two. The reference's
			// `idx_idx_opt` (:495-497) has no one-index arm for these mnemonics, so this cannot
			// arise from legal text; a refusal rather than a padded zero keeps the impossible case
			// loud.
			return errf(mnemonic, "cannot yet encode %s with one index (#8)", mnemonic.Text)
		}
		// The sugar arm. The written index is the **second** category — elem for `table.init`,
		// data for `memory.init` — and it is written **first** on the wire, the defaulted table or
		// memory index 0 following it.
		if err := p.retainIdxIn(mnemonic, first, cats.second); err != nil {
			return err
		}
		p.appendImm(encodeLocalIdx(0))
		return nil
	}
	if initReversedKinds[mnemonic.Keyword] {
		// Second written, first encoded.
		if err := p.retainIdxIn(mnemonic, second, cats.second); err != nil {
			return err
		}
		return p.retainIdxIn(mnemonic, first, cats.first)
	}
	if err := p.retainIdxIn(mnemonic, first, cats.first); err != nil {
		return err
	}
	return p.retainIdxIn(mnemonic, second, cats.second)
}

// idxPairRetained parses the two mandatory index immediates of an `idx idx` arm and retains both.
//
// **The seven `immIdxIdx` mnemonics need no per-mnemonic split for a NUMERIC field index, and
// that is a finding rather than a simplification taken for granted.** Five resolve an ordinary
// module-level pair — `ARRAY_COPY` `{catType, catType}`, `ARRAY_NEW_ELEM`/`ARRAY_INIT_ELEM`
// `{catType, catElem}`, `ARRAY_NEW_DATA`/`ARRAY_INIT_DATA` `{catType, catData}` — and the other
// two, `STRUCT_GET` and `STRUCT_SET`, pass `{catType, catFieldOfType}`, whose second category has
// no module-level space (`idxSpaceFor` returns nil for it, by design — see `catFieldOfType`'s
// comment). What makes a uniform reader sufficient for the numeric case is `retainIdxIn`'s own
// numeric fast path: a **numeric** field index is encoded straight from its value and never
// reaches `idxSpaceFor` at all, so `struct.get 0 1` and `struct.set $vec 0` retain through the
// identical call a `table.copy` pair does.
//
// **A symbolic field name is the one case `idxSpaceFor` cannot answer, and it is split off to
// `structFieldPairRetained` rather than folded into the uniform path (#188).** `struct.wast` has
// six `assert_return` vectors (`get_vec_y`, `set_get_y`, `set_get_1`, and the three that
// share their module) using `(struct.get $vec $y (local.get $v))` — a symbolic field name
// resolved against the field space *of the struct type the first index names*, per
// `catFieldOfType`'s comment. `compType.fieldNames` (typetable.go) is where `structtype`'s
// per-struct binding now survives past its own function, and `structFieldPairRetained` is its
// one reader.
//
// None of the seven mnemonics reverses (`initReversedKinds` names only `TABLE_INIT`/`MEMORY_INIT`,
// the sugar-arm pair), so the write order is the parse order throughout, unlike `retainIdxPair`
// below.
func (p *parser) idxPairRetained(mnemonic Token) error {
	first, err := p.idxValue()
	if err != nil {
		return err
	}
	second, err := p.idxValue()
	if err != nil {
		return err
	}
	cats := pairCategories(mnemonic.Keyword)
	if cats.second == catFieldOfType {
		// The only two mnemonics that reach here are STRUCT_GET and STRUCT_SET — see
		// idxPairLookupKinds — and both need the first index's *value*, not merely its
		// category, before the second can resolve. Splitting them out here rather than
		// widening retainIdxIn keeps every other category's resolution uniform: catType's
		// resolution for the five ordinary pairs above is untouched.
		return p.structFieldPairRetained(mnemonic, first, second)
	}
	if err := p.retainIdxIn(mnemonic, first, cats.first); err != nil {
		return err
	}
	return p.retainIdxIn(mnemonic, second, cats.second)
}

// structFieldPairRetained retains STRUCT_GET/STRUCT_SET's `idx idx` pair, resolving the second
// index against the *specific* struct type the first names (#188).
//
// **Why this cannot go through `retainIdxIn`'s uniform path.** Every other category `idxSpaceFor`
// answers is a single module-level space, so `retainIdxIn` needs only the *category* to resolve a
// symbolic index. `catFieldOfType` is different by construction — the reference's own production
// is `let x = $2 c type_ in $1 x ($3 c (field x.it)).it` (parser.mly:622) — `field x.it` looks up
// the per-struct field space that belongs to type `x`, and `x` is not knowable until the *first*
// index has actually been resolved to a number. So the second index's resolution depends on the
// first's value, not merely on a category constant, which is the one respect in which this pair
// differs from `table.init`'s `{catTable, catElem}` (two categories, but neither depends on the
// other's value) or `array.copy`'s `{catType, catType}` (one category, shared).
//
// **The first index resolves exactly as it would through the uniform path.** `catType` is never
// `catFunc` or `catLocal`, so `retainIdxIn`'s branches for those two categories never apply to it
// — its only behavior there is `space.resolveSpaceIdx(r)` against `&p.ctx.types`, which
// `p.ctx.resolveTypeIdx` performs directly (typetable.go), reading the identical map. Calling it
// here rather than through `retainIdxIn` avoids resolving the same name twice for no reason: this
// function needs the resolved *value* regardless, to hand to the field lookup, so there is nothing
// left for the generic wrapper to add.
//
// **The forward-referencing sub-case defers the whole pair, and it used to refuse.** `struct.get
// $futuretype $field` naming a struct type defined later in the module is legal wat — nothing in the
// grammar requires a type to precede its use by a non-function-typed index any more than
// `imports.wast:62` requires a function's type to — and both halves were rejected at the cursor:
// `unknown type $s` for a symbolic type index, and a `#188` refusal for a symbolic field name whose
// type index had not been parsed yet. Both measured, both in the reverse field order only. That is
// grave #130's shape and it was tracked under a second number because the refusal read as a frontier;
// the mechanism that fixes the index categories fixes it too, so it is fixed here.
//
// **The pair defers as one component**, not as two, because the second index's resolution depends on
// the *first index's value* rather than on a category: `field x.it` looks up the field space of the
// type `x` names, so the two cannot be separated the way `table.init`'s `{catTable, catElem}` can.
// `retainMemarg`'s shape, for the same reason stated there.
//
// The suite has no vector either way — a scan of every `struct.get`/`struct.set` in the corpus (all
// 254 files) found the type-naming index always resolving to a type already fully defined earlier in
// the same module, for the six symbolic-field vectors and for every numeric-field one — so this moves
// no board column and is certified by its own derived vector, with the reference encoder beside it.
func (p *parser) structFieldPairRetained(mnemonic Token, first, second idxRef) error {
	if !p.retaining() {
		return nil
	}
	build := func() ([]byte, error) {
		typeIdx, err := p.ctx.resolveTypeIdx(first)
		if err != nil {
			return nil, err
		}
		fieldIdx, err := p.resolveFieldIdx(mnemonic, typeIdx, second)
		if err != nil {
			return nil, err
		}
		var w writer
		w.bytes(encodeLocalIdx(typeIdx))
		w.bytes(encodeLocalIdx(fieldIdx))
		return w.b, nil
	}
	if first.isVar || second.isVar {
		// Either name may be bound after this instruction, and a symbolic *field* needs its type's
		// definition to have been parsed — both of which stage 2 has and the cursor does not. Two
		// numeric indices need no space at all (`nat32` performs no lookup), so they keep the cursor
		// path and their error timing with it.
		p.deferImm(build)
		return nil
	}
	b, err := build()
	if err != nil {
		return err
	}
	p.appendImm(b)
	return nil
}

// resolveFieldIdx resolves STRUCT_GET/STRUCT_SET's second index against the field space of the
// struct or array type `typeIdx` names (#188).
//
// A numeric field index needs no resolution — `retainIdxIn`'s numeric fast path, reproduced here
// because this function is the second index's *only* path once its category is known to be
// `catFieldOfType`; there is no generic wrapper left to supply it.
//
// The symbolic case needs `typeIdx` to already be defined: `p.ctx.typeDefs` is appended in source
// order as each `(type …)` field is parsed (`defineType`, typetable.go), synchronously with the
// name binding `resolveTypeIdx` just consulted — both happen inside one call to `typeDef`, so if
// the name resolved, the definition it names has already been appended.
//
// **The bounds check's meaning changed when the caller started deferring, and its message with it.**
// It used to catch a numeric type index naming a type this parse had not reached yet, which is why it
// cited a forward reference; `structFieldPairRetained` now runs this in stage 2, where every `(type
// …)` field has been parsed, so an index past the end is simply **out of range** — a module naming
// type 99 where 2 exist. Same branch, different population, and the wrong word there would be a
// refusal claiming a frontier for an ill-formed module.
//
// A type that resolves but is a `compFunc` or a fieldless `compArray` — the grammar never lets an
// array bind a field name, `arraytype` having no `bindidx` arm — falls into the same "not found"
// branch as a genuinely unknown name: `fieldNames` is nil for both, and a nil map read reports
// "not found" exactly as an empty one would, so there is nothing to special-case.
func (p *parser) resolveFieldIdx(mnemonic Token, typeIdx uint32, r idxRef) (uint32, error) {
	if !r.isVar {
		return r.idx, nil
	}
	if typeIdx >= uint32(len(p.ctx.typeDefs)) {
		return 0, errf(r.tok,
			"%s names field %s of type %d, and this module defines %d types (#188)",
			mnemonic.Text, r.tok.Text, typeIdx, len(p.ctx.typeDefs))
	}
	if i, ok := p.ctx.typeDefs[typeIdx].fieldNames[r.name]; ok {
		return i, nil
	}
	// The reference's own message, `lookup "field " …` (parser.mly:162-163) — the trailing space
	// is the same quirk `lookupLabel` reproduces for `label `, and for the identical reason: the
	// suite reads only as far as its expected string, and no vector in the corpus reaches this
	// branch (measured — every struct.get/struct.set in the corpus names a bound field), so this
	// is ours alone to keep honest against the authority rather than against a vector.
	return 0, errf(r.tok, "unknown field  %s", r.tok.Text)
}

// constImmRetained is constImm plus the encoded immediate.
//
// The range check and the encoding come from the same conversion (num.go's `*ConstBits` pair), which
// is what stops a literal from passing the check and being written wrong — the drift
// `fitsAsIntConst`'s face-comment describes.
func (p *parser) constImmRetained(mnemonic Token) error {
	t := p.c.peek()
	if err := p.constImm(mnemonic); err != nil {
		return err
	}
	if !p.retaining() {
		return nil
	}
	bits, isFloat := constWidth(mnemonic.Text)
	b, ok := constImmBytes(t, bits, isFloat)
	if !ok {
		// Unreachable: constImm's range check uses the same conversion, so a token that passed it
		// converts. A panic would be the honest response to a broken invariant, but this is a
		// parser and the invariant's other half is one function away — an error naming the
		// disagreement is diagnosable where a panic in a library is not.
		return errf(t, "internal: %s passed the range check and failed to encode", t.Text)
	}
	p.appendImm(b)
	return nil
}

// brTable reads `br_table`'s whole label sequence and encodes it as the wire form.
//
// **The wire form is not the written form, and that is this function's entire content.** The text is
// `br_table l1 … ln default` — one `idx` then an `idx_list` (parser.mly:497, :563-565) with *no
// count* and no marker separating the members from the default. The encoding is
// `op 0x0e; vec labelidx; labelidx` (encode.ml:250-ish, `BrTable (xs, x) → op 0x0e; vec idx xs;
// idx x`), so three transformations happen between them:
//
//   - **The last written label is the default**, not a member: the reference is
//     `Lib.List.split_last ($2 c label :: $3 c label)`, which splits the *whole* sequence and takes
//     its final element. So `br_table 0 1 2` is a two-member vector `[0 1]` with default `2`, and a
//     reader that treated the first index as the default (the shape the grammar's `idx idx_list`
//     invites) would encode `br_table 2 1 0`.
//   - **A count precedes the members**, which the parser does not have until the sequence ends.
//     This is why `br_table` cannot share the leading `labelIdx` read with `br` and `br_if`: an
//     index retained on sight lands where the count belongs.
//   - **The sequence is never empty.** `idx idx_list` requires at least one index, and that one is
//     the default — `br_table 0` encodes as an empty vector plus default 0, three bytes rather
//     than two. An emitter writing only what it saw would produce a two-byte instruction the
//     decoder reads as `br_table` with a *count* of 0 and then no default at all, consuming the
//     next instruction's opcode as one.
//
// The labels are resolved as they are read (`labelIdxValue`) rather than retained as references: a
// label's scope is lexical and the enclosing blocks are on the stack *now*, which is #80's reason
// and does not change with the buffering.
//
// Not retaining is not a special case that skips the read — the recognizer path still parses the
// whole sequence, and only the encoding is suppressed.
func (p *parser) brTable() error {
	// The leading `idx` is mandatory; the tail is `idx_list`, whose empty arm is the lookahead.
	first, err := p.labelIdxValue()
	if err != nil {
		return err
	}
	// **Appended per label, never sized from a count**, there being no count in the text to be
	// talked into: the vector's length is bounded by the tokens read, exactly as `textFunc.locals`
	// is (see its comment on #138's exposure).
	tail, err := p.labelIdxList()
	if err != nil {
		return err
	}
	depths := append([]uint32{first}, tail...)
	if !p.retaining() {
		return nil
	}
	// split_last: the members are everything but the last, the default is the last.
	labels, deflt := depths[:len(depths)-1], depths[len(depths)-1]
	p.appendImm(encodeLocalIdx(uint32(len(labels))))
	for _, d := range labels {
		p.appendImm(encodeLocalIdx(d))
	}
	p.appendImm(encodeLocalIdx(deflt))
	return nil
}

// textFunc is one defined function's retained body.
//
// `typeIdx` is resolved in stage 2 like every other signature, so it is a thunk rather than a
// number: `deferSignature` already owns the type-interning question and this must not answer it a
// second time. `locals` is the *flattened* valtype vector — one entry per local, not per run —
// because the text source spells locals individually (`(local i32 i32)` is two entries) and the
// RLE is applied on the way out by funcLocalBytes.
//
// **It is deliberately still flat now that `binary.Func.Locals` is not** (#138). The old reason
// given here was that the decoder flattened too, so comparing flat made the round trip
// meaningful; that reason expired and the field did not, because the *source* is what this
// struct holds and the source is a list. The parser has no count to be talked into: a text
// module declaring a million locals writes a million tokens, so this vector's size is bounded by
// the input's size — which is exactly the property the decoder lost when it expanded a five-byte
// LEB. Same shape, different exposure, and worth stating so the next reader does not
// "consistency-fix" this into groups and inherit a round-trip that cannot see run boundaries.
type textFunc struct {
	typeIdx func() (uint32, error)
	// locals are unresolved, because `(local (ref null $t))` may name a type defined later —
	// see funcField's tail and funcLocalBytes.
	locals []valType
	// kw is the field's `func` keyword, for the position an encodability refusal quotes. A local's
	// own valtype token would be better and is not retained: `valtype` returns a value, and threading
	// a token out of it to improve one message's offset is a change to eleven callers for a field the
	// refusal already names.
	kw   Token
	body instrSink
}

// funcLocalBytes resolves and encodes one function's locals, in the deferred phase.
//
// **Both halves have to happen here rather than at the cursor.** Resolution is deferred because a
// local may name a forward-referenced type; encodability is asked *with* it because `valTypeBytes` is
// one function answering both questions (encode.go's argument), and splitting them would be a second
// place knowing which types have bytes.
//
// It refuses per function rather than per module, and quotes the local's ordinal, because that is
// what a reader edits — the same reason `encodableOrErr` refuses per import rather than folding the
// valtype check into one loop.
//
// **One `[]byte` per local, not one byte** — since `valTypeBytes` widened, a local's encoded type may
// be more than one byte (a parameterized reference's prefix plus its heaptype), so the flat `[]byte`
// this used to build (one byte per local, appended positionally) can no longer represent the general
// case. `writeLocals` folds these into runs by slice equality now, not byte equality.
func (p *parser) funcLocalBytes(f textFunc) ([][]byte, error) {
	out := make([][]byte, 0, len(f.locals))
	for i, v := range f.locals {
		rv, err := p.ctx.resolveVal(v)
		if err != nil {
			return nil, err
		}
		b, ok := valTypeBytes(rv)
		if !ok {
			return nil, errf(f.kw, "cannot yet encode local %d: %s needs a parameterized reference "+
				"encoding, which arrives with the GC gate (#8)", i, rv)
		}
		out = append(out, b)
	}
	return out, nil
}

// opBytes returns a mnemonic's opcode byte sequence.
//
// The table is generated from the reference (`opcodes.go`, decision 0014), so this is a lookup and
// not a decision. A mnemonic absent from the table has no encoding, and that is reported to the
// caller rather than defaulted: a default opcode is how an unencodable instruction would emit
// *some other instruction* and decode clean.
//
// Ambiguous mnemonics are refused here rather than guessed. `ambiguousOpcodes` holds the three
// whose encoding depends on their operands' types (`select`, `ref.test`, `ref.cast`), which is
// validation's knowledge and not this stratum's — so they stay unencodable and say so. None of them
// occurs in the slice this section is scoped to, and a wrong guess would be accept-direction.
func opBytes(mnemonic string) ([]byte, bool) {
	if _, ambiguous := ambiguousOpcodes[mnemonic]; ambiguous {
		return nil, false
	}
	e, ok := mnemonicOpcodes[mnemonic]
	if !ok {
		return nil, false
	}
	var w writer
	if e.prefix == 0 {
		w.byte1(byte(e.code))
		return w.b, true
	}
	// A prefixed opcode is `prefix` then the sub-opcode as a **u32 LEB**, not as a bare byte:
	// `encode.ml` writes `op 0xfc; u32 0x0cl` (:596), and a sub-opcode above 127 therefore takes
	// two bytes. Writing it as one byte is correct for every sub-opcode the slice reaches and
	// wrong in general, which is the shape of defect this project treats as worse than a failure.
	w.byte1(e.prefix)
	w.u32(e.code)
	return w.b, true
}

// encodedFunc is one function after every deferred question has been answered: a type index, the
// RLE-able locals, and the body's bytes up to but not including the terminator.
//
// **It exists so that the two writers cannot fail.** The resolutions a function needs — its type
// index, a forward-referenced `call $f`, a local's heap type — all belong to the deferred phase, and
// a writer that performed them inline would have to abandon a half-written section on error. Every
// other section in this emitter is written by a `func(*writer)` that cannot fail, because
// `encodableOrErr` asked its questions first; this type is what lets sections 3 and 10 keep that
// property rather than becoming the one place an error can leave bytes behind.
type encodedFunc struct {
	typeIdx uint32
	// locals is one encoded valtype per local, not yet folded into runs — `[][]byte` rather
	// than `[]byte` since `funcLocalBytes` widened, because a parameterized reference's
	// encoding is more than one byte. `writeLocals` does the RLE fold at write time.
	locals [][]byte
	body   []byte
}

// resolveFuncs answers every deferred question the function and code sections have, or reports the
// first that cannot be answered.
//
// Run before a single byte of either section is written, which is `encodableOrErr`'s discipline
// extended to the two sections whose content is resolved rather than merely present.
func (p *parser) resolveFuncs() ([]encodedFunc, error) {
	out := make([]encodedFunc, 0, len(p.funcs))
	for _, f := range p.funcs {
		idx, err := f.typeIdx()
		if err != nil {
			return nil, err
		}
		locals, err := p.funcLocalBytes(f)
		if err != nil {
			return nil, err
		}
		var fb writer
		for _, in := range f.body.instrs {
			fb.bytes(in.op)
			imm := in.imm
			if in.patch != nil {
				// A `call $f` whose target is now bound. The patch *replaces* the immediates rather
				// than appending to them, and it is the whole immediate because `finishImm` composed
				// every component into it — the positions that used to be missing here are inside
				// that closure, which is grave #130's repair and the reason the refusals this
				// paragraph used to cite are gone.
				b, perr := in.patch()
				if perr != nil {
					return nil, perr
				}
				imm = b
			}
			fb.bytes(imm)
		}
		out = append(out, encodedFunc{typeIdx: idx, locals: locals, body: fb.b})
	}
	return out, nil
}

// resolveTagDefs answers every deferred question the tag section has, or reports the first that
// cannot be answered — `resolveFuncs`' own shape, for the identical reason: a section 13 writer
// must be `func(*writer)`-shaped and unable to fail, so the thunks run here rather than inline.
func (p *parser) resolveTagDefs() ([]uint32, error) {
	out := make([]uint32, 0, len(p.ctx.tagDefs))
	for _, thunk := range p.ctx.tagDefs {
		idx, err := thunk()
		if err != nil {
			return nil, err
		}
		out = append(out, idx)
	}
	return out, nil
}

// writeTagSection writes section 13: one type index per defined tag, each preceded by the fixed
// zero attribute byte (`tagtype`, encode.ml:190-191: `TagT ut -> u32 0x00l; typeuse u32 ut`).
//
// **The zero byte is the wire's, not a placeholder for something this stratum doesn't know.**
// `types.ml:40`'s `tagtype = TagT of typeuse` carries nothing beyond the type index — the
// attribute is fixed at 0 in the current spec and there is no second tag-attribute value to
// choose between, so writing a literal `0x00` here is complete rather than provisional, on
// exactly the reasoning `mutability`'s two fixed bytes already rest on.
func writeTagSection(w *writer, tagIdxs []uint32) {
	w.section(secTag, func(body *writer) {
		body.vec(len(tagIdxs), func(bw *writer, i int) {
			bw.byte1(0x00)
			bw.u32(tagIdxs[i])
		})
	})
}

// writeFuncSection writes section 3: one type index per defined function.
//
// Sections 3 and 10 are *both* derived from the same function list, exactly as `encode.ml` derives
// them from `m.it.funcs` at :1141 and :1159 — `func_section` writing `idx x` per function and
// `code_section` writing the bodies. Fed from one list here for the same reason: two lists would be
// two places knowing how many functions there are, and a disagreement between them is a module
// whose code section describes bodies for functions the type section never declared.
func writeFuncSection(w *writer, funcs []encodedFunc) {
	w.section(secFunction, func(body *writer) {
		body.vec(len(funcs), func(bw *writer, i int) { bw.u32(funcs[i].typeIdx) })
	})
}

// writeCodeSection writes section 10: each function's locals and body.
//
// The per-body length prefix is `encode.ml`'s `gap32`/`patch_gap32` pair (:1029-1039) — a gap
// written, the body encoded, the measured distance patched back — and the nested writer below is the
// same mechanism with the seek removed. Measuring rather than predicting keeps the "never compute a
// length twice" property the writer's header states.
//
// **Every body ends with an explicit `0x0b` the text does not spell.** `code` calls `end_ ()` after
// its instruction list (:1035), so the terminator is the encoder's to add; a text `(func)` with an
// empty body still encodes as one byte. Omitting it makes the decoder read the next function's
// locals as this one's instructions, which is the wrong-layer error this project's graves are full
// of.
func writeCodeSection(w *writer, funcs []encodedFunc) {
	w.section(secCode, func(body *writer) {
		body.vec(len(funcs), func(bw *writer, i int) {
			// The entry is built in a nested writer so its length is measured. `writer.section`
			// cannot serve here — a code entry is length-prefixed *without* an id byte — so the
			// splice is written out rather than shoehorned into a helper whose shape differs.
			var fb writer
			writeLocals(&fb, funcs[i].locals)
			fb.bytes(funcs[i].body)
			fb.byte1(opEnd)
			bw.u32(uint32(len(fb.b)))
			bw.bytes(fb.b)
		})
	})
}

// opEnd is the `end` opcode every function body is terminated with.
//
// Named rather than written as a literal `0x0b` at the one site that needs it, because it is *not*
// this file's fact to state: it is the decoder's. Cross-checked by
// TestEndOpcodeMatchesTheDecodersOpinion, so a literal here cannot drift from the decoder's own
// opinion of which byte ends a block.
//
// **That check is behavioural, and the name says so because an earlier draft of this comment cited a
// table comparison that could not be written.** `optable.go` holds the `0x0b` row and is unexported
// in `internal/binary`, so reading it from here would mean widening a package's surface to let a test
// make an assertion. Asking the decoder whether a body terminated with this byte decodes — and
// whether any of the other 255 also do — obtains the same fact from the authority instead of from a
// second copy of it. This comment previously cited `TestEndOpcodeMatchesTheTable`, which never
// existed, for three commits (#114/#115/#116); `TestEveryCitedTestNameResolves` is what said so.
const opEnd byte = 0x0b

// opElse is the `else` opcode separating an `if`'s two arms.
//
// **It has no row in the generated table, and the absence is structural rather than an omission.**
// `opcodes.go` is joined from the reference's grammar and lexer on *constructor names*
// (decision 0014), and `optable.go`'s rows for `0x05` and `0x0b` carry a `reason` — "misplaced ELSE
// opcode", "misplaced END opcode" — with **no mnemonic**, because neither byte is an instruction the
// text grammar spells. `else` and `end` are delimiters of a production, not members of
// `plaininstr`. So there is nothing for the join to key on and `mnemonicOpcodes` cannot have a row
// for either; a literal here is the only honest source, exactly as it is for `opEnd`.
//
// Cross-checked the same behavioural way, and the discriminator is *specific to this byte*:
// `TestElseOpcodeMatchesTheDecodersOpinion` asks the decoder which bytes are legal inside an `if`
// and illegal inside a `block`, and finds exactly one — measured over all 256, count 1. That is a
// sharper question than "does a body containing this byte decode", which `nop` also passes.
const opElse byte = 0x05

// writeLocals writes a function's locals as the run-length-encoded vector the format requires.
//
// `encode.ml:238-242` is a **right fold**: `combine` merges a local into the head run when the
// types match, so runs form from the right and the result is `vec local` over `(count, type)`
// pairs. A left fold produces the same runs for every input — the merge condition is symmetric —
// but the direction is stated because the reference's is, and a reader checking this against
// :238 should find the same shape rather than an equivalent one.
//
// The input is flattened (one entry per local) and the output is RLE, which is the asymmetry
// `textFunc.locals` documents.
//
// **The round trip now compares the RLE, and that is a strengthening rather than a bookkeeping
// change** (#138). While the decoder flattened, the comparison could not distinguish "wrote one
// run of two" from "wrote two runs of one" — both decode to the same flat vector, and only one is
// what `combine` produces. The reason given here for that being fine was that the RLE is "purely
// an encoding detail" and comparing it would compare our grouping choice to itself; the second
// half was true and the first was not, since the grouping is `encode.ml:238`'s and therefore the
// reference's rather than ours. With `binary.Func.Locals` retaining runs, the decoder is an
// independent reader of them, so the round trip checks the fold's output against what the format
// says those bytes mean. encode_test.go's three-group case is where that bites.
//
// **`locals` is `[][]byte`, and the run-length fold compares by `slices.Equal`, not `==`**, since
// `valTypeBytes` widened: a local's encoded type may now be several bytes (a parameterized
// reference's prefix plus its heaptype), so two locals of the same type produce two byte slices
// that must be compared element-wise to fold into one run — a `[]byte`-keyed comparison would
// treat every multi-byte local as its own run of one, encoding a well-formed local list correctly
// but never as `combine` (:238) would.
func writeLocals(w *writer, locals [][]byte) {
	type run struct {
		n uint32
		t []byte
	}
	var runs []run
	for _, t := range locals {
		if len(runs) > 0 && slices.Equal(runs[len(runs)-1].t, t) {
			runs[len(runs)-1].n++
			continue
		}
		runs = append(runs, run{n: 1, t: t})
	}
	w.vec(len(runs), func(bw *writer, i int) {
		bw.u32(runs[i].n) // `len n` (:239)
		bw.bytes(runs[i].t)
	})
}

// encodeLocalIdx encodes a resolved index as a `u32` immediate.
//
// Trivial, and named anyway: it is the one place an index immediate's *width and signedness* are
// decided, and `encode.ml`'s `idx` is `u32` for every index category (:571). An index written as
// an `s32` would encode identically for small values and differ above 63, which is the silent
// divergence class this project spends its comments on.
func encodeLocalIdx(v uint32) []byte {
	var w writer
	w.u32(v)
	return w.b
}

// The two `select` opcodes, which `opBytes` cannot supply and which are named here for that reason.
//
// **`ambiguousOpcodes` holds both and cannot say which is which.** The generated table joins on
// *constructor* names (decision 0014) and the reference has one constructor, `select`, for both
// encodings — so the row is `{{0x00, 0x1b, "select"}, {0x00, 0x1c, "select"}}` and nothing in it
// distinguishes the annotated form from the bare one. Reading the pair positionally would be
// depending on the order `OpsOf` happened to append them in, which is `decode.ml`'s arm order and
// is not a fact the table states. So the two bytes are named here, and the table is used as
// *corroboration* rather than as the source: TestSelectOpcodesMatchTheGeneratedTable asserts the
// pair is exactly these two, so a table that changed would fail rather than be silently overridden.
//
// Which byte carries the vector is a behavioural fact and is checked as one
// (TestSelectOpcodesMatchTheDecodersOpinion): a body of just `0x1b` decodes, and a body of just
// `0x1c` is `unexpected end of section or function`, because `0x1c` is followed by `vec valtype ts`
// (encode.ml:249). Measured against the decoder, not read off a comment.
const (
	// opSelect is `Select None` — no immediates (encode.ml:248).
	opSelect byte = 0x1b
	// opSelectT is `Select (Some ts)`, followed by `vec valtype ts` (encode.ml:249).
	opSelectT byte = 0x1c
)

// The four `ref.test`/`ref.cast` opcodes, named for `refCastOpBytes`' reason — `opBytes` cannot
// supply them, and `ambiguousOpcodes` holds each pair without saying which member is which
// (`select`'s comment above states the same fact about that map's shape).
//
// **The choice here is the operand's *nullability*, not syntax** — the opposite of `select`'s
// case. `select`'s two encodings are told apart by whether `(result …)` was *written*, a fact
// available at the cursor with no type information at all; `ref.test`/`ref.cast` choose by
// whether the reftype *is* nullable, per `encode.ml:421-424`:
//
//	RefTest (NoNull, t) -> op 0xfb; op 0x14; heaptype t
//	RefTest (Null, t)   -> op 0xfb; op 0x15; heaptype t
//	RefCast (NoNull, t) -> op 0xfb; op 0x16; heaptype t
//	RefCast (Null, t)   -> op 0xfb; op 0x17; heaptype t
//
// which is exactly the fact `reftypeRetained` already has in hand (the `valType.null` bit its
// `p.reftype()` call resolved) and `opBytes` structurally cannot: it is handed a bare mnemonic
// string, one layer below where the operand's type is known. This is `opBytes`'s ambiguousOpcodes
// refusal's own comment naming the reason these two stay refused there — decidable here, where
// the type is in hand, and nowhere lower.
const (
	opRefTestNonNull byte = 0x14 // ref_test (NoNull, t)
	opRefTestNull    byte = 0x15 // ref_test (Null, t)
	opRefCastNonNull byte = 0x16 // ref_cast (NoNull, t)
	opRefCastNull    byte = 0x17 // ref_cast (Null, t)
)

// refCastOpBytes chooses `ref.test`/`ref.cast`'s opcode from the mnemonic and the resolved
// reftype's nullability.
//
// `mnemonic.Keyword` rather than `.Text`, matching every other opcode lookup in this file, so a
// spelling variant cannot silently fail to match `"REF_TEST"`/`"REF_CAST"`.
func refCastOpBytes(mnemonic Token, null bool) ([]byte, bool) {
	switch mnemonic.Keyword {
	case "REF_TEST":
		if null {
			return []byte{0xfb, opRefTestNull}, true
		}
		return []byte{0xfb, opRefTestNonNull}, true
	case "REF_CAST":
		if null {
			return []byte{0xfb, opRefCastNull}, true
		}
		return []byte{0xfb, opRefCastNonNull}, true
	default:
		// Every other keyword is a caller error rather than a case this function partitions:
		// `reftypeRetained`'s only two callers pass exactly these two kinds. Written as `default`
		// rather than left implicit so `exhaustive` reads this as a real fallback over
		// `keywordKind`'s whole vocabulary — the same discipline `flatSelectOrCall`'s switch uses,
		// two arms out of many and a stated catch-all for the rest.
		return nil, false
	}
}

// selectOpByte chooses between the two, on whether a `(result …)` was **written**.
//
// **Not on whether it contained anything.** `selectinstr_results_instr_list` (parser.mly:682-686)
// returns `true, snd $3 c @ ts, es` for the parenthesized arm and `false, [], $1 c` otherwise, and
// `select (if b then (Some ts) else None)` spends `b` — so `select (result)` is `Some []`, which
// encodes as `0x1c` with a zero-length vector. A predicate of `len(results) > 0` would encode that
// as a bare `0x1b`: a *different instruction*, decoding clean and validating differently. Hence the
// boolean travels beside the slice from the reader to here rather than being recovered from it.
//
// `(result)` with an empty valtype list is legal text — `valtype_list` has an empty arm
// (:396-398) — and no vector in the corpus writes it, which is exactly why the flag is the rule and
// the length is not.
func selectOpByte(wrote bool) []byte {
	if wrote {
		return []byte{opSelectT}
	}
	return []byte{opSelect}
}

// selectResultBytes encodes `select (result …)`'s `vec valtype ts` (encode.ml:249), in the deferred
// phase.
//
// **Deferred for `funcLocalBytes`' reason, not for a new one.** A result may be `(ref null $t)`
// naming a type defined later in the field list, so resolving at the cursor would reject a legal
// module — the accept-direction failure this package's index resolutions are already split over. So
// this is reached through `instr.patch`, and the *encodability* question is asked here with the
// resolution because `valTypeBytes` answers both and splitting them would be a second place knowing
// which types have bytes.
//
// It refuses per result and quotes the ordinal, which is `funcLocalBytes`' shape for the same reason:
// that is what a reader edits.
func (p *parser) selectResultBytes(results []valType, tok Token) ([]byte, error) {
	var w writer
	w.u32(uint32(len(results)))
	for i, v := range results {
		rv, err := p.ctx.resolveVal(v)
		if err != nil {
			return nil, err
		}
		b, ok := valTypeBytes(rv)
		if !ok {
			return nil, errf(tok, "cannot yet encode select result %d: %s needs a parameterized "+
				"reference encoding, which arrives with the GC gate (#8)", i, rv)
		}
		w.bytes(b)
	}
	return w.b, nil
}

// typeuseIdxBytes encodes a `typeuse s33` (encode.ml:108 with :68): an **s33**, not a u32.
//
// `s33 i = s64 (extend_i32_s i)` (:68) and `typeuse idx = function Idx x -> idx x` (:108) — so the
// index is sign-extended from 32 bits and written as a *signed* LEB. That is not a stylistic
// difference from `encodeLocalIdx`: the two encodings agree for indices below 64 and diverge at 64,
// where a u32 writes one byte (`0x40`) and an s33 writes two (`0xc0 0x00`).
//
// **Two productions use it and the blocktype one is where the divergence bites**, which is why the
// lesson stays written here rather than moving with the name. `blocktype` is
// `typeuse s33 (Idx x.it)` (:229-230), and `0x40` is precisely the empty-blocktype marker — so a u32
// there would encode `(block (type 64) …)` as a block with no signature at all: a well-formed module
// denoting something else, on a module with 64 types, which no vector in the corpus reaches.
// `heaptype`'s `UseHT ut -> typeuse s33 ut` (:133) is the second caller, and it is the *same*
// production rather than a same-shaped one — a second copy of this three-line function would be
// grave #105's re-derivation, where a sibling next door is a place to read and not a place to
// invent. It was named `typeuseIdxBytes` while it had one caller; the rename came with the second.
//
// The sign extension is `int64(int32(idx))`, which is a no-op for every index the format admits
// (an index is a u32 and only its low 31 bits can be a legal one) and is written the reference's
// way regardless: the alternative is a cast that is *incidentally* right, which is the kind of
// agreement that stops being true when the input changes.
func typeuseIdxBytes(idx uint32) []byte {
	var w writer
	w.s64(int64(int32(idx)))
	return w.b
}

// blockTypeSlot is a blocktype's stage-2 result: the interned index, or the fact that this
// signature needed none.
//
// `interned` is **not** derivable from `idx`, which is why it is a field: index 0 is legal, so a
// zero value cannot mean "no type". It is the same reason `deferSignature`'s slot carries `filled`
// beside its index, and the same hazard — a premature or absent read encoding a *legal* index and
// decoding clean.
type blockTypeSlot struct {
	idx      uint32
	interned bool
	filled   bool
}

// blockTypeBytes returns the opener's immediate thunk: the three forms of `blocktype`
// (encode.ml:229-232), chosen by what stage 2 actually did.
//
// **The choice is read from the interner's own answer rather than recomputed here.** `interned` is
// set by the same call that decided whether to append to the type space, so the encoder cannot
// disagree with the space about whether a type exists — see internBlockImplicit. Recomputing
// `inlineBlockType()` at this site would be the second copy that rule exists to prevent.
//
// Deferred because the index is a stage-2 fact (a `(type $t)` may name a type defined later, and an
// implicit signature's slot is not known until every explicit type is in the table), and because a
// single result may be `(ref null $t)` whose resolution is deferred for the same reason
// `funcLocalBytes` is.
//
// The unfilled case reports rather than returning a plausible zero, exactly as `deferSignature`'s
// thunk does: `0x40`-versus-index is the difference between a block with no signature and a block
// naming type 0, both of which decode.
func (p *parser) blockTypeBytes(slot *blockTypeSlot, ft funcType, tok Token) func() ([]byte, error) {
	return func() ([]byte, error) {
		if !slot.filled {
			return nil, errf(tok, "internal: blocktype read before stage 2 resolved it")
		}
		if slot.interned {
			return typeuseIdxBytes(slot.idx), nil
		}
		// `ValBlockType None` — the `([], [])` arm.
		if len(ft.results) == 0 {
			return []byte{blockTypeEmptyByte}, nil
		}
		// `ValBlockType (Some t)` — `([], [t])`, written as a `valtype` and **not** as an s33:
		// `blocktype`'s third arm is `valtype t` (encode.ml:232), and `valtype`'s own grammar is
		// `decodeBlockTypeValue`'s third branch — `d.decodeValType`, the same production a table's
		// element type or a global's type goes through — so a parameterized reference result
		// (`(ref i31)`, `(ref null $t)`) is exactly as legal here as the negative-s7 single-byte
		// forms, and `valTypeBytes` returns however many bytes that form needs.
		rv, err := p.ctx.resolveVal(ft.results[0])
		if err != nil {
			return nil, err
		}
		b, ok := valTypeBytes(rv)
		if !ok {
			return nil, errf(tok, "cannot yet encode a block whose result is %s: it needs a "+
				"parameterized reference encoding, which arrives with the GC gate (#8)", rv)
		}
		return b, nil
	}
}

// blockTypeEmptyByte is `s33(-0x40)` — the `ValBlockType None` form (encode.ml:231).
//
// A single byte rather than a call to `sleb`, and the equivalence is asserted rather than assumed:
// `-0x40` is `0x40` in one signed LEB byte (its bit 6 is set, so it sign-extends to -64, which is
// the off-by-one `writer.sleb`'s own comment describes). TestBlockTypeFormsMatchTheReference pins
// this against the writer, so the literal cannot drift from the encoder that would compute it.
const blockTypeEmptyByte byte = 0x40

// constImmBytes encodes an `iN.const`/`fN.const` immediate from its token.
//
// The four arms are `encode.ml:576-579` — `I32 → s32 c`, `I64 → s64 c`, `F32 → f32 c`,
// `F64 → f64 c` — and the two halves of each row matter independently: the *width* comes from the
// mnemonic (constWidth) and the *signedness* from the arm. Ints are signed LEBs, floats are raw
// little-endian words of the width, and a float written as a LEB would be a valid encoding of a
// different instruction.
//
// The conversions return **bit patterns** (num.go), and the sign interpretation happens here at the
// width the mnemonic named: `int32(uint32(v))` for i32 so `i32.const 4294967295` and
// `i32.const -1` — the same i32, two spellings — both write the single byte `7f`. Doing the
// interpretation in the converter instead would force it to choose one spelling's reading and
// reject the other, which is the accept-direction failure `intConstBits`' comment records.
func constImmBytes(t Token, bits uint, isFloat bool) ([]byte, bool) {
	var w writer
	if isFloat {
		n, ok := floatConstBits(t.Text, bits)
		if !ok {
			return nil, false
		}
		if bits == 32 {
			w.f32(uint32(n))
		} else {
			w.f64(n)
		}
		return w.b, true
	}
	n, ok := intConstBits(t.Text, bits)
	if !ok {
		return nil, false
	}
	if bits == 32 {
		w.s32(int32(uint32(n)))
	} else {
		w.s64(int64(n))
	}
	return w.b, true
}

// The locals path deliberately has no encodability helper of its own: it calls `valTypeBytes`, the
// same predicate the five existing sections use. A second opinion about which value types can be
// encoded is what #111's six remaining sites are the standing reminder of, and a wrapper adding
// nothing but a name would be the first step toward one.
