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
// # Why patchLocal exists rather than a deferred byte slice
//
// A `call $f` cannot encode its immediate at parse time, so `imm` is left empty and `patch` records
// the resolution to run in stage 2. The alternative — deferring the whole instruction — would put
// an instruction's *position* in a thunk, and position is the one thing this list exists to fix.
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

// appendImm adds encoded immediate bytes to the instruction being built, or does nothing when not
// retaining.
func (p *parser) appendImm(b []byte) {
	if p.sink == nil {
		return
	}
	p.imm = append(p.imm, b...)
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
	if h.abs == "" && h.tok.Kind == VarTok {
		// The forward-referencing arm. `retainIdxIn`'s `catFunc` shape, copied rather than
		// re-derived — including both of its guards, which are structural here rather than
		// reachable: `ref.null` has exactly one immediate, so neither a second patch nor a
		// preceding immediate can exist. They are asserted anyway, because the invariant a patch
		// *replaces* the immediates is the thing `resolveFuncs` relies on, and a shape that grew a
		// second immediate would otherwise silently drop it.
		if p.immPatch != nil || len(p.imm) != 0 {
			return errf(tok, "cannot yet encode a deferred heap type beside another immediate (#8)")
		}
		p.immPatch = func() ([]byte, error) {
			return p.heaptypeBytesOf(mnemonic, h, tok)
		}
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
// than of this call.** A numeric index needs no resolution at all. A symbolic index in a space whose
// names are all bound before use — locals, whose space `funcSignature` resets and fills per function
// — resolves at the cursor. A symbolic index in a space that permits forward references — funcs,
// where `call $f` may precede `(func $f)` — cannot, so it defers through `immPatch`.
//
// Deferring *only* the categories that need it is not an optimization: `p.ctx.locals` is reset per
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
	// **The locals space may be short, and this is the one place that can tell.** A typeuse with no
	// re-stated signature contributes its referenced type's params as anonymous locals, and this
	// stratum cannot count them (`funcField`, #77) — so every symbolic local in the body resolves one
	// slot too low. `(func (type $sig) (local $var i32) (local.get $var))` encoded `$var` as **0**
	// where `$sig`'s param owns 0: a well-formed image denoting a different function, which the wabt
	// corpus caught at one byte and no suite vector can see.
	//
	// Refused *here* rather than at the func, because the narrow predicate is the honest one. Refusing
	// every typeuse costs `(module (func (type $t)) (type $t (func)))`, whose `$t` has no params and
	// whose body is fine — a row already in the round-trip table. What is actually unencodable is a
	// symbolic *local* read against a space that may be missing entries, and a numeric one is not
	// affected: `local.get 0` means slot 0 whatever the space holds, so it is the resolution and not
	// the typeuse that is blocked. The error names the typeuse token all the same, that being the
	// field the engine cannot write.
	//
	// **It still over-refuses, and by how much is stated rather than left to be inferred.** A typeuse
	// naming a *param-less* type is harmless — `(type $t (func))` binds nothing, so `$v` really is slot
	// 0 — and this refuses it anyway, because the param count is what cannot be known at the cursor
	// and "does it have params" is the same unanswerable question as "how many". So the predicate is
	// deliberately the coarser one on the side that refuses; a frontier that occasionally declines an
	// encodable module is a cost, where one that occasionally writes a wrong index is a defect no
	// vector can see. #77's `force_locals` machinery is what makes the question answerable.
	if cat == catLocal && p.localsMissParams {
		return errf(p.localsMissParamsTok,
			"cannot yet encode a symbolic local in a func whose typeuse supplies its params (#77)")
	}
	if cat == catFunc {
		// Forward references are legal here, so the resolution is stage 2's. One patch per
		// instruction, which plaininstr consumes — an instruction with two symbolic indices in
		// forward-referencing spaces would need more, and none exists in the slice this section
		// reaches; the pair path below refuses rather than silently keeping one.
		if p.immPatch != nil {
			return errf(r.tok, "cannot yet encode two deferred indices on one instruction (#8)")
		}
		before := len(p.imm)
		if before != 0 {
			return errf(r.tok, "cannot yet encode a deferred index after another immediate (#8)")
		}
		p.immPatch = func() ([]byte, error) {
			idx, err := p.ctx.funcs.resolveSpaceIdx(r)
			if err != nil {
				return nil, err
			}
			return encodeLocalIdx(idx), nil
		}
		return nil
	}
	// Every other category resolves **at the cursor**, which is one pass where the reference has
	// two — `module_fields1`'s second closure resolves all of them, and the `catFunc` arm above is
	// that closure built for exactly one category because forward calls made the need unmissable.
	// The premise for the rest ("its definitions precede the code section") is a fact about the
	// *image*: `module_fields` admits any field order in the source, so
	// `(module (func (data.drop $s)) (data $s …))` is rejected here and accepted upstream, while the
	// reverse order is accepted here. Grave #130 — an accept-direction defect no vector can see,
	// which is where the both-orders control scoped to all the categories lives.
	idx, err := space.resolveSpaceIdx(r)
	if err != nil {
		return err
	}
	p.appendImm(encodeLocalIdx(idx))
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
				// than appending to them, which is why retainIdx refuses an instruction that would
				// need both: one deferred index and one immediate would need a position, and a
				// position is what the byte slice does not carry.
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
