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
// This lands the **type section** and nothing else, which is a vertical slice rather than a stub:
// the type space is the one part of a module that is fully retained on the parse side and fully
// represented on the decode side today, so it round-trips end to end and can be checked against the
// cross-check corpus. A wider surface would need retention that does not exist yet, and building
// that retention *and* its consumer in one motion shapes a representation in the load-bearing spot.
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
// parser-accepted text modules this encodes **15** in full, 10 of which the corpus can be joined to
// — and those 10 agree **byte for byte**, 0 disagreements. The 5 unjoined all live in
// `annotations.wast`, which the manifest names as skipped with its reason, so the gap is accounted
// for rather than merely stated.
//
// That figure is quoted with its own vacuity check attached, because *exactly zero* on an agreement
// is the tell grave #106 was filed for. Nine of the ten are 8-byte bare preambles and prove nothing
// about the type section. The witness is the tenth: `type.wast#0`, 23 type definitions and 148
// bytes, identical to an image produced by a toolchain that has never seen this parser. One real
// agreement is the honest claim here — not ten.
//
// Byte equality is *evidence*, deliberately not the criterion: the corpus is an authority on which
// module the text denotes, not on encoding style, which is why #67's comparator compares `[]Instr`.
// A future divergence in a legal-but-different encoding is a fact about wabt's style and must not be
// read as a defect here.

// secType is the type section's id. The other twelve ids arrive with their sections — naming them
// now would be twelve constants nothing reads, which is the placeholder shape #6 rules on, and the
// honest version of "later" here is an empty line rather than a tracked stub.
const secType byte = 1

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
	p, err := parseModule(src)
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
func parseModule(src []byte) (*parser, error) {
	c, err := newCursor(src)
	if err != nil {
		return nil, err // a lex error, unwrapped; see newCursor
	}
	p := &parser{c: c}
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
		return errf(p.ctx.firstNonType, "cannot yet encode a (%s …) field: the emitter writes the "+
			"type section only (#8)", p.ctx.firstNonType.Text)
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
