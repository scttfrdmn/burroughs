package binary

import "fmt"

// Section payload decoding — "the decoder stops taking sections' word for it".
//
// # The size mechanism
//
// A section declares a byte length. Verifying it is one mechanism with three
// faces, and which face fires is decided by *where* the disagreement shows up.
// All three are the same question — does the grammar agree with the declared
// extent — asked at three different moments:
//
//  1. The declared length exceeds the bytes actually present in the image:
//     ErrSectionOverrun, "length out of bounds". Checked before any grammar runs.
//  2. The grammar wants a byte and the *image* is exhausted: ErrPayloadEnd,
//     "unexpected end of section or function".
//  3. The grammar finishes and the byte count it consumed differs from the
//     declared length, in either direction: ErrSectionSizeMismatch,
//     "section size mismatch".
//
// Face 3 is the two-signed one, and the tests pin both signs independently
// (TestSectionSizeBothSigns): a sign error there would swap nothing visible in
// the pass count, because both directions are malformed either way. Only the
// error *text* would be wrong, which is exactly the kind of defect a pass count
// cannot see.
//
// # Payload grammar is not bounded by the section
//
// Face 2 says "the image is exhausted", not "the section is exhausted", and that
// is deliberate — it is what the suite's vectors demand, and getting it wrong
// looks plausible in both directions:
//
//   - binary.wast:754 has an export section declaring 2 exports and supplying 1,
//     with a code section following. The grammar reads on past the section end
//     into the code section's id byte, takes it as a name length (0x0a = 10),
//     finds fewer than 10 bytes left in the *image*, and reports "length out of
//     bounds". A section-bounded reader would have stopped at the boundary and
//     said "unexpected end of section or function" instead.
//   - binary.wast:92 is the same story told by the suite itself, in a comment:
//     the reference interpreter "consumes the \0b (data section start) as an END
//     instruction and reports the code section as being larger than declared".
//     Over-reading past the boundary is how face 3 comes to fire at all.
//
// So the grammar runs against the whole remaining image and the extent is
// reconciled afterwards. The one place that needs the boundary back is the
// custom section, which is why it carries its own check — see decodeCustom.

// Features gates which proposals' constructs the decoder accepts.
//
// This is the acceptance layer, and it is a different layer from the grammar.
// *Malformed* belongs to the grammar of the tracked union and is gate-blind
// (CLAUDE.md, "gates never manufacture malformedness"): section id 13 is
// well-formed because Wasm 3.0 defines it, and id 14 is malformed because
// nothing tracked does — neither fact moves when a gate flips. What a gate
// decides is whether a well-formed construct is *accepted*, and a gate-off
// engine meeting a gated construct rejects it with a feature-named error, never
// with a spec malformed-string it has no right to.
//
// The zero value is v0's posture: every 3.0 gate present and off (contract §9).
type Features struct {
	// The comments name every construct the gate governs, because a gate whose scope is
	// only known at its check sites is a gate nobody can audit — and writing them out is
	// how #48 was found. See that issue: the instruction grammar dispatches from the
	// generated table with no feature check anywhere, so with these gates off the decoder
	// *accepts* gated opcodes in function bodies, which is the accept-and-ignore the
	// gates-never-manufacture-malformedness ruling forbids in its other direction.
	//
	// So these comments describe the gates as they *are*, and the missing opcode scope is
	// tracked rather than described as present.
	// The opcode half of every scope below is now real, and it lives in gatemap.go
	// rather than here: the mapping is hand-authored testimony with a citation per
	// entry, and this struct is where the gates are *declared*, not where their scope is
	// enumerated. Decision 0008.
	ExceptionHandling bool // tag section (id 13), import/export kind 4; throw, throw_ref, try_table
	SIMD              bool // v128 value type, including as a blocktype; the 0xfd region
	Threads           bool // shared limits flags (2, 3)
	Memory64          bool // 64-bit limits flags (4..7)

	// The four gates #48 found missing. A *tracked* proposal (contract §9 G-2) with no
	// bool here is worse than a gate that never fires, because the reflection-derived
	// lanes cannot exercise a gate that is not there to reflect over — the
	// forgotten-fifth-gate scenario existing in the wild, four times.
	GC          bool // the 0xfb region; ref.eq, and the function-references five (0008)
	TailCall    bool // return_call, return_call_indirect
	RelaxedSIMD bool // the fd 0x100..0x12f window, inside SIMD's region
	MultiMemory bool // memarg flags bit 6: an explicit memory index on loads and stores

	// ExtendedConst governs no opcode of its own, and that is the whole shape of it: the six
	// instructions it admits — i32/i64 add, sub, mul — are MVP instructions that are *ungated
	// everywhere else*. What the proposal widens is a **position**, the set of instructions legal
	// in a constant expression, so this gate lives in `gatedNonOpcodes` and governs `constOps`.
	// A `gatedOpcodes` entry would be read by `gateCheck` on every dispatch path and would
	// decline `i32.add` inside ordinary function bodies, which is a valid module rejected.
	//
	// The gate this file forgot, and #109 is why it is a nine rather than an eight: G-2's
	// parenthetical named six of Wasm 3.0's ten features and extended-const was not among them,
	// so `constOps`' comment could assert these arrived "with their gates" and be believed. They
	// had no gate. Nine suite modules the spec requires accepted were rejected with `constant
	// expression required`, and no board could see it — every one of the 4162 green vectors is a
	// rejection (§9 G-3). Found by #67's cross-check corpus.
	ExtendedConst bool // i32/i64 add, sub, mul in a constant expression — a position, not an opcode
}

// Decoder holds the configuration one decode runs under. A config struct rather
// than a widening parameter list: per-section acceptance is gate-dependent for
// every section that will ever be added, so the gate set is a property of the
// decoder, not an argument to one function.
type Decoder struct {
	Features Features

	// sawDataRef records that a decoded function body used an opcode whose free
	// variables include the data index space (see dataRefOps). It is per-decode state
	// on the Decoder rather than a return value because the question it answers is
	// asked at the *module* level — `require (data_count <> None || ... datas =
	// Set.empty)` (decode.ml:1299) — and reset at the top of DecodeModule so a reused
	// Decoder cannot carry one module's answer into the next. That reset is the
	// stateful-instrument law (#28) applied to the decoder itself: state that survives
	// a measurement reports history.
	sawDataRef bool

	// m is the module under construction, and it is per-decode state for exactly
	// sawDataRef's reason — reset at the top of DecodeModule so a reused Decoder
	// cannot carry one module's retained form into the next.
	//
	// **This field is the producer seam's answer** (see module.go). The descent grows
	// the representation as it recognizes, rather than a second pass rebuilding it from
	// the payloads the first pass aliased, and sawDataRef is the precedent: state on the
	// Decoder, written from the bottom of the grammar, read at module level. A second
	// pass would be a second grammar over the same bytes, drifting silently from this
	// one — the risk 0006 says to prefer away from.
	//
	// The consequence, stated because it constrains every function below: `decode*`
	// signatures stay **error-only**. 4162 of the suite's vectors are rejections, and a
	// rejection path whose shape changes is a rejection path whose behaviour has to be
	// re-proven. So a production that retains something writes it here and still returns
	// only an error.
	//
	// Written unconditionally, including on paths that go on to fail: a decode that
	// returns an error discards the module, so a partially-filled m is never observed.
	// Guarding each write on eventual success would mean buffering, which is the second
	// pass wearing a different hat.
	//
	// **Never read directly — go through mod().** Every retaining production is also
	// reachable from a test that drives it without a surrounding DecodeModule, and the
	// first version of this field was read directly on the assumption that the module
	// grammar is the only caller. That assumption is false in the tree *today*
	// (rectype_test.go:191 calls decodeCompType with a bare Decoder) and it made a nil
	// dereference, not a wrong answer — so the panic was found. The lesson is the one that
	// generalizes: a retention target that only exists on one entry point's path makes
	// every production's correctness depend on who called it, and the productions are
	// deliberately callable in isolation because that is how their rejection direction is
	// pinned. mod() removes the precondition rather than documenting it.
	m *Module

	// funcTypeIdx holds the function section's type indices until the code section's
	// bodies arrive to be zipped with them.
	//
	// A staging field rather than writing directly into m.Funcs, because the two halves
	// of a Func are in two different sections and the *function* section comes first —
	// so at the time the indices are read there are no bodies to attach them to.
	// checkCounts already requires the two counts to agree, but it runs at the *end* of
	// DecodeModule, after both grammars: so this pairing cannot assume agreement and
	// zips defensively, keeping whichever half is short. A module whose halves disagree
	// is rejected by checkCounts moments later and its retained form is discarded.
	funcTypeIdx []uint32

	// valType is the value type the most recent successful `valtype` read accepted.
	//
	// An out-parameter on the Decoder rather than a return value, and that is forced
	// rather than chosen: `decodeValType` is passed as a `func(*reader) error` to both
	// `either` and `decodeVec`, so widening its signature would widen theirs — and
	// `either`'s signature is the shape of a *backtracking alternation*, which has no
	// meaningful value to return for a branch that failed.
	//
	// Written only immediately before a successful return, never on a path that goes on
	// to fail. That ordering is what makes it safe under `either`: a branch that
	// backtracks has not written, so the value standing after the alternation belongs to
	// the branch that actually matched. A write-then-fail would leave a type from a
	// production the module never contained — the invented-evidence class (grave #36) in
	// a field instead of a message.
	valType ValType

	// blockType is the encoded blocktype the most recent `blocktype` read accepted, on
	// the Decoder for valType's reason — its alternation is an `either`. See
	// decodeBlockTypeValue for the encoding and for why the three forms cannot collide.
	blockType uint64
}

// mod returns the module the descent retains into, creating it if this Decoder was driven
// below DecodeModule.
//
// The lazy creation is not convenience — it is what keeps retention's correctness
// independent of the entry point. Every production that retains something is also called
// directly by a test pinning its rejection direction, and those tests construct a bare
// `&Decoder{}`; requiring a caller to have set up a module first would mean the retaining
// half of each production is only exercised through one door, which is the shape of a
// precondition that excuses its own check. A module built here is discarded with the
// Decoder, so nothing observes it.
func (d *Decoder) mod() *Module {
	if d.m == nil {
		d.m = &Module{}
	}
	return d.m
}

// featureErr names the gate, never the grammar. The text deliberately does not
// resemble any assert_malformed string in the suite: a gate-off engine that
// spoofed a spec string would score itself green for rejecting a module the spec
// calls well-formed, which is the suite measuring the wrong thing.
func featureErr(feature string) error {
	return fmt.Errorf("%s: %w", feature, ErrFeatureDisabled)
}

// decodePayload runs the grammar for one section over r, which is positioned at
// the first payload byte and bounded only by the image.
//
// A section with no grammar here yet returns false: the caller skips its payload
// and no extent check runs, because an extent cannot be checked against a
// grammar that does not exist. That is the declared-and-tracked form of "not
// done" (CLAUDE.md) — the alternative, a grammar that consumes `size` bytes and
// declares victory, would report agreement it never verified.
func (d *Decoder) decodePayload(sid SectionID, size uint32, r *reader) (bool, error) {
	switch sid {
	case SectionCustom:
		return true, d.decodeCustom(size, r)
	case SectionType:
		return true, d.decodeVec(r, d.decodeRecType)
	case SectionImport:
		return true, d.decodeVec(r, d.decodeImport)
	case SectionFunction:
		return true, d.decodeVec(r, d.decodeFuncTypeIdx)
	case SectionTable:
		return true, d.decodeVec(r, d.decodeTable)
	case SectionMemory:
		return true, d.decodeVec(r, d.decodeMemory)
	case SectionExport:
		return true, d.decodeVec(r, d.decodeExport)
	case SectionStart:
		// A bare function index, not a vec.
		idx, err := r.u32()
		if err != nil {
			return true, err
		}
		d.mod().Start, d.mod().HasStart = idx, true
		return true, nil
	case SectionDataCount:
		// A bare u32, not a vec. Its value is cross-checked in checkCounts; here
		// it only has to be present and well-formed.
		_, err := r.u32()
		return true, err
	case SectionTag:
		// Ranked by the structural layer (it is well-formed), accepted only by
		// the gate. Its payload grammar arrives with the EH gate (#95).
		//
		// The citation was `(#8)` until the deferral sweep that followed #22: #8 is the
		// wat-harness issue and owns none of this, so the deferral was declared but in
		// substance *untracked* — a tracking number that cannot be followed to the work,
		// which is the gap the `ErrTrailingData` ruling's declared-and-tracked test exists
		// to close rather than a grave.
		if !d.Features.ExceptionHandling {
			return false, featureErr("exception handling")
		}
		return false, nil
	case SectionGlobal:
		return true, d.decodeVec(r, d.decodeGlobal)
	case SectionElement:
		return true, d.decodeVec(r, d.decodeElemSegment)
	case SectionData:
		return true, d.decodeVec(r, d.decodeDataSegment)
	case SectionCode:
		// The last section to get a grammar (#39), and the one #22 was waiting on.
		//
		// Each body carries its own `sized` extent (decode.ml:1140), so the per-body
		// mismatch is reported inside decodeFuncBody and the section-level extent check
		// still runs on top — two levels of the same mechanism, which is why
		// binary.wast:92 is a `section size mismatch` about a *function*.
		return true, d.decodeVec(r, d.decodeFuncBody)
	default:
		// Unreachable: every id in sectionRank has a case above, and an id absent from
		// sectionRank was rejected as malformed before the payload was reached.
		// Declared rather than silent — a section added to the rank table without a
		// grammar arrives here, and returning false would let it decode as an
		// unchecked skip.
		return false, fmt.Errorf("%w: %d has no payload grammar", ErrMalformedSectionID, byte(sid))
	}
}

// discardIndex reads an index and drops it. Index *validity* — does the function
// exist — is the validator's question, not the decoder's; here the only claim is
// that a well-formed u32 occupies those bytes.
//
// Still the right reader at the sites that keep it — a subtype's declared supertypes, a
// br_table's label vector, an explicit memory index — where the index is read to prove
// the field is well-formed and nothing yet consumes its value. Those are #7's remaining
// gaps, named at their call sites.
func discardIndex(r *reader) error {
	_, err := r.u32()
	return err
}

// decodeFuncTypeIdx reads one entry of the function section: a type index, staged until
// the code section's bodies arrive.
//
// The two halves of a Func live in two sections and the function section comes first, so
// there is nothing to attach an index to when it is read. See Decoder.funcTypeIdx.
func (d *Decoder) decodeFuncTypeIdx(r *reader) error {
	idx, err := r.u32()
	if err != nil {
		return err
	}
	d.funcTypeIdx = append(d.funcTypeIdx, idx)
	return nil
}

// decodeVec applies an element grammar `count` times. The count is a u32 read
// from the head of the payload, and a truncated element is face 2 of the size
// mechanism, reported by the reader itself.
func (d *Decoder) decodeVec(r *reader, elem func(*reader) error) error {
	n, err := r.u32()
	if err != nil {
		return err
	}
	for range n {
		if err := elem(r); err != nil {
			return err
		}
	}
	return nil
}

// decodeCustom reads a custom section's name and skips the rest.
//
// This is the one grammar that needs the section boundary the others discard,
// and custom.wast:76 is why. That vector is an empty custom section (`\00\00`,
// declared length zero) followed by a well-formed one. Reading the name without
// the boundary would take the *next* section's id byte as a zero-length name,
// then decode the following section cleanly and accept the module — the suite
// says "unexpected end". The name lives inside the declared extent, so
// over-reading it is an error even though over-reading a type or export vec is
// not: the difference is that a custom section's tail is opaque bytes, so there
// is no later grammar step for face 3 to catch the over-read with.
func (d *Decoder) decodeCustom(size uint32, r *reader) error {
	start := r.off
	if err := r.name(); err != nil {
		return err
	}
	rest := int(size) - (r.off - start)
	if rest < 0 {
		return ErrPayloadEnd
	}
	_, err := r.bytes(rest)
	return err
}

// decodeRecType reads a rectype — the type section's actual element (decode.ml:273-276,
// reached from `type_` at :1023).
//
// **This engine decoded `functype` here and called it the section** (#86). The reference's
// grammar is four levels deep, and only `comptype`'s first arm existed:
//
//	rectype   →  0x4e vec(subtype)  |  subtype                        :273-276
//	subtype   →  0x50 | 0x4f  vec(typeuse) comptype  |  comptype       :262-271
//	comptype  →  -0x20 functype | -0x21 struct | -0x22 array          :250-259
//	fieldtype →  storagetype mutability                               :243-246
//
// The five missing forms were reported as `malformed function type: 0x5f` and friends —
// which is the #51 class twice over. They are Wasm 3.0 constructs, so they belong to the
// tracked union's grammar (§9 G-2) and the engine's own configuration is what declines
// them: *gates never manufacture malformedness*. And `malformed function type` is a string
// the reference **never produces** (0 hits in `third_party/spec/interpreter/`), so the
// fallthrough was an invented sentinel, not merely a mis-scoped one.
//
// `peek` rather than `either` for the 0x4e discriminator, matching the reference: it uses
// `peek` + `skip 1` (:274) precisely because a rectype group's *contents* must not be
// re-judged as a bare subtype when a nested read fails. An `either` here would rewind and
// report the second branch's error for a well-formed group with a bad member.
func (d *Decoder) decodeRecType(r *reader) error {
	if b, ok := r.peek(); ok && b == -0x32&0x7F { // 0x4e — `rec`
		// A recursive type group is GC's, and the gate is checked before descending for
		// decodeRefType's reason: otherwise the member read reports the error and it
		// reports the wrong layer.
		if !d.Features.GC {
			return featureErr("gc")
		}
		r.skip(1) // the peeked discriminator — `skip 1 s` (decode.ml:275)
		return d.decodeVec(r, d.decodeSubType)
	}
	return d.decodeSubType(r)
}

// decodeSubType reads a subtype: an optional supertype list, then a comptype
// (decode.ml:262-271).
//
// Both explicit forms carry `vec(typeuse u32)` — the declared supertypes — and differ only
// in finality, which decoding does not observe. Peeked for decodeRecType's reason.
func (d *Decoder) decodeSubType(r *reader) error {
	if b, ok := r.peek(); ok && (b == -0x30&0x7F || b == -0x31&0x7F) { // 0x50, 0x4f
		if !d.Features.GC {
			return featureErr("gc")
		}
		r.skip(1) // `skip 1 s` (decode.ml:264, :268)
		// `vec (typeuse u32) s` — the supertypes, as plain type indices.
		if err := d.decodeVec(r, discardIndex); err != nil {
			return err
		}
	}
	return d.decodeCompType(r)
}

// decodeCompType reads a comptype: functype, structtype, or arraytype (decode.ml:250-259).
//
// The form tag is a *signed* LEB of width 7, not a plain byte, and
// binary-leb128.wast:1067 is the vector that says so: `\e0\7f` is -0x20 encoded
// in two bytes, and the suite wants "integer representation too long" rather
// than a malformed-form error. The spec's type constructors live in negative
// s7 space — 0x60 *is* -32 at width 7, as 0x5e (array) is -34 — so reading the
// tag as a byte gets the right answer for well-formed input and the wrong error
// for an overlong encoding of it.
//
// sleb(7) is exactly the right instrument: its width budget is one byte, so a
// continuation bit on the first byte exhausts it, which is what "too long" means
// here. Verified against the reference sN at width 7 (grave #36's port).
func (d *Decoder) decodeCompType(r *reader) error {
	form, err := r.sleb(7)
	if err != nil {
		return err
	}
	switch form {
	case -0x20: // 0x60 — functype
		// Retained, and the two vectors are read into the two fields rather than by the
		// shared `decodeVec(d.decodeValType)` the loop used to run twice. The loop is
		// gone because retention is the one thing the two halves do *not* share: they
		// are the same grammar writing to different destinations, and a loop over two
		// destinations is a loop with a branch in it.
		var ft FuncType
		for _, dst := range [...]*[]ValType{&ft.Params, &ft.Results} {
			if err := d.decodeVec(r, func(r *reader) error {
				if err := d.decodeValType(r); err != nil {
					return err
				}
				*dst = append(*dst, d.valType)
				return nil
			}); err != nil {
				return err
			}
		}
		d.mod().Types = append(d.mod().Types, CompType{Kind: CompFunc, Func: ft})
		return nil

	case -0x21: // 0x5f — structtype: a vector of fieldtypes
		if !d.Features.GC {
			return featureErr("gc")
		}
		if err := d.decodeVec(r, d.decodeFieldType); err != nil {
			return err
		}
		// The slot is taken and the contents are not retained — fieldtypes have no
		// representation yet and nothing consumes them (#7). Taking the slot is the
		// part that matters: a struct type occupies a type index, so skipping it here
		// would shift every later index in the all-gates-on lane and nowhere else.
		d.mod().Types = append(d.mod().Types, CompType{Kind: CompStruct})
		return nil

	case -0x22: // 0x5e — arraytype: exactly one fieldtype
		if !d.Features.GC {
			return featureErr("gc")
		}
		if err := d.decodeFieldType(r); err != nil {
			return err
		}
		d.mod().Types = append(d.mod().Types, CompType{Kind: CompArray})
		return nil
	}
	// GRAVE (#36): the message names the byte the image actually held, which at
	// width 7 is the low seven bits of the decoded value — the range is -64..63, so
	// form&0x7f is exactly the input byte, and a multi-byte encoding never reaches
	// here (sleb(7) has already returned "too long"). The first version of this
	// expression or'd a high bit in for every negative form and reported 0x5e
	// (array) as 0xde: an error about the module lying about the module, which no
	// suite vector here can catch, this one's expected string being the bare
	// sentinel — the harness reads exactly as far as the expected string does, and
	// where a spec string embeds a value (`illegal opcode ff`) the rendering *is*
	// oracle-covered (#38). Found by *printing* the output for nine tags rather than
	// reading the expression's shape. Pinned by TestCompTypeFormIsASignedLEB.
	//
	// The sentinel is `malformed definition type` (:259) and **was** `malformed function
	// type`, which the reference never emits anywhere — a fabricated sentinel where #36
	// was a fabricated byte, invisible for the same reason: no vector asserts either
	// string, so the board could not tell them apart (#86).
	return fmt.Errorf("%w: %#02x", ErrMalformedDefType, byte(form&0x7F))
}

// decodeFieldType reads a fieldtype: storage type then mutability (decode.ml:243-246).
func (d *Decoder) decodeFieldType(r *reader) error {
	if err := d.decodeStorageType(r); err != nil {
		return err
	}
	// The mutability bit is read and dropped: fieldtypes are not retained (#7, and see
	// CompType), so there is nothing to record it on. The *read* is what matters — it is
	// the check `binary-gc.wast` scores.
	_, err := d.decodeMutability(r)
	return err
}

// decodeStorageType reads a storagetype: a valtype or a packed type (decode.ml:236-241).
//
// `either`, as the reference has it, and the ordering matters for the same reason
// decodeBlockType's does: the valtype branch runs first and its failure must not stand,
// since the cursor rewinds and the bytes get judged again as a packtype. The packtype
// branch is last, so its message — `malformed storage type` (:234) — is the one `either`
// returns for a byte that is neither.
func (d *Decoder) decodeStorageType(r *reader) error {
	return either(r, d.decodeValType, func(r *reader) error {
		form, err := r.sleb(7)
		if err != nil {
			return err
		}
		if form != -0x08 && form != -0x09 { // i8, i16
			return fmt.Errorf("%w: %#02x", ErrMalformedStorageType, byte(form&0x7F))
		}
		return nil
	})
}

// decodeMutability reads the mutability byte — `mutability` (decode.ml:154-158).
//
// **One function with two call sites, which is the whole point.** The reference calls it
// from `fieldtype` (:244) and from `globaltype` (:294), and this engine had transcribed it
// at the global position only — so `binary-gc.wast`'s array-field mutability byte was never
// read, and that vector was the board's last remaining fail. Grave #83's shape exactly: one
// production in the reference, called from two arms, copied at one of them. Written as a
// shared function *before* the second copy could exist rather than factored out after
// (#86).
func (d *Decoder) decodeMutability(r *reader) (bool, error) {
	mut, err := r.byte()
	if err != nil {
		return false, err
	}
	if mut > 0x01 {
		return false, fmt.Errorf("%w: %#02x", ErrMalformedMutability, mut)
	}
	return mut == 0x01, nil
}

// decodeValType reads a value type — `valtype` (decode.ml:220-225).
//
// `either [numtype; vectype; reftype]`, and the third branch is why this is a three-way
// alternation rather than the flat seven-byte switch it was. **The engine had two readers
// for one production and the second was narrower**: `reftype` accepts fourteen forms and
// the switch accepted `0x70`/`0x6F`, so `anyref`, `eqref`, `i31ref`, `structref`,
// `arrayref`, the four `null*ref`s, `exnref` and both parameterized `(ref ht)` forms were
// all reported `malformed value type` — an accept-direction defect, invisible to a board
// whose vectors are all `assert_malformed` (#88, grave #83's shape at a third site).
//
// Three properties of the reference's shape are load-bearing and none of them is obvious:
//
//   - **The message for a byte that is no valtype is `malformed reference type`**, because
//     `either` returns the last branch's error and `reftype` is last. Not "value type",
//     which the reference never emits, and not "number type", which is the *first*
//     branch's. Pinned by TestValTypeAlternationIsTheReference.
//   - **numtype/vectype read `s7`, not a byte** (:167-177), so `\ff\7f` is
//     `integer representation too long` from whichever branch runs, and the flat switch's
//     `r.byte()` would have called it a bogus form. Same width lesson as grave #36.
//   - **The gate lives in `vectype`'s branch, and `either` must not swallow it.** With
//     SIMD off, `0x7b` is a decline this alternation would previously have overwritten
//     with the reftype branch's malformed-string; `either` propagates `ErrFeatureDisabled`
//     as of #86, which is what makes this decomposition safe at all. The order is *not*
//     available as the remedy here, because the reference fixes it and the last branch is
//     the one whose message must stand.
func (d *Decoder) decodeValType(r *reader) error {
	return either(r, d.decodeNumType, d.decodeVecType, d.decodeRefType)
}

// decodeNumType reads a number type — `numtype` (decode.ml:167-172).
func (d *Decoder) decodeNumType(r *reader) error {
	form, err := r.sleb(7)
	if err != nil {
		return err
	}
	switch form {
	case -0x01, -0x02, -0x03, -0x04: // i32 i64 f32 f64
		d.valType = ValType(form & 0x7F)
		return nil
	}
	return fmt.Errorf("%w: %#02x", ErrMalformedNumType, byte(form&0x7F))
}

// decodeVecType reads a vector type — `vectype` (decode.ml:174-177).
//
// One form, and the SIMD gate. The gate is *here* rather than at the alternation because
// the reference puts the form here: a v128 with SIMD off is a well-formed Wasm 3.0
// construct this configuration declines, so it is feature-named, and the enclosing
// `either` propagates rather than backtracks that (#5, #86).
func (d *Decoder) decodeVecType(r *reader) error {
	form, err := r.sleb(7)
	if err != nil {
		return err
	}
	if form != -0x05 { // v128
		return fmt.Errorf("%w: %#02x", ErrMalformedVecType, byte(form&0x7F))
	}
	if !d.Features.SIMD {
		return featureErr("simd")
	}
	d.valType = V128
	return nil
}

// decodeRefType reads a reference type, as a table's element type — `reftype`
// (decode.ml:201-218).
//
// The reference reads a signed LEB at width 7 and accepts **fourteen** forms, of which
// this engine's original two (0x70 funcref, 0x6F externref) are the Wasm 2.0 subset. The
// other twelve are GC's — the abstract heap types (-0x0c..-0x0f, -0x12..-0x17) and the
// two *parameterized* prefixes -0x1c (ref, non-null) and -0x1d (ref null), each of which
// is followed by a `heaptype`. Reading them as `malformed reference type` was #51's
// accept-direction defect: the spec defines them, so the engine's own configuration is
// what declines them, and it must say so (*gates never manufacture malformedness*).
//
// A signed LEB, not a byte, and that is the reference's choice rather than a flourish:
// grave #36's lesson is that the width decides the *error string* for an overlong
// encoding, and `sleb(7)` reports "too long" where a byte read would report a bogus
// form. The message names the input byte via form&0x7F, which at width 7 is exact for
// the same reason decodeFuncType's does (range -64..63) — the reconstruction that grave
// found lying is not repeated here.
func (d *Decoder) decodeRefType(r *reader) error {
	form, err := r.sleb(7)
	if err != nil {
		return err
	}
	switch form {
	case -0x10, -0x11: // funcref (0x70), externref (0x6F) — Wasm 2.0, ungated
		d.valType = ValType(form & 0x7F)
		return nil

	case -0x0C, -0x0D, -0x0E, -0x0F, // noexn, nofunc, noextern, none
		-0x12, -0x13, -0x14, -0x15, -0x16, -0x17: // any, eq, i31, struct, array, exn
		// GC's abstract heap types. Well-formed per Wasm 3.0, so the decline is
		// feature-named (decision 0008 folds function references into the GC gate).
		if !d.Features.GC {
			return featureErr("gc")
		}
		// Accepted, and **not representable** in ValType — so the sentinel is written
		// rather than the field left alone. Leaving it would let the previous read's
		// type stand as this one's answer, which is grave #36's class in a field instead
		// of a message: an engine reporting a value its input never held. The all-gates-on
		// CI lane is what makes this reachable, so it is not a hypothetical arm.
		d.valType = NoValType
		return nil

	case -0x1C, -0x1D: // (ref ht), (ref null ht)
		// The parameterized forms, each followed by a heaptype. The gate is checked
		// *before* descending, because the heaptype read would otherwise be the thing
		// that reports the error and it would report the wrong layer.
		if !d.Features.GC {
			return featureErr("gc")
		}
		if err := d.decodeHeapType(r); err != nil {
			return err
		}
		d.valType = NoValType
		return nil
	}
	return fmt.Errorf("%w: %#02x", ErrMalformedRefType, byte(form&0x7F))
}

// decodeHeapType reads a heap type — `heaptype` (decode.ml:178-198).
//
// An `either` alternation in the reference: a type *index* (s33, so a plain funcidx-style
// number) or one of the eleven abstract forms. Written as one here for the reason
// decodeBlockType is: on an overlong LEB the first branch's error must not stand, since
// the cursor rewinds and the bytes get judged again.
//
// **It carries its own gate checks, and that changed with #88.** The previous comment here
// said the function is "reached only with the GC gate on — decodeRefType checks the gate
// before descending", and declined a second check on the reasoning that two checks for one
// construct is the accept-and-ignore hazard's mirror. That reasoning was sound and its
// premise stopped being true: `immHeapType` now reads this function directly (instr.go),
// from `ref.null`/`ref.test`/`ref.cast`, and `ref.null` is a Wasm 2.0 opcode with no gate of
// its own — so the gate state at entry is no longer known.
//
// The two checks are not the same fact checked twice, which is what makes keeping both
// honest. decodeRefType gates the `-0x1c`/`-0x1d` **prefix**, a construct GC introduces
// whatever heaptype follows it; the checks below gate the *forms*, per decision 0008's
// folding of function-references into the GC gate:
//
//   - the **type index** branch is function-references — `ref.null 0` is not Wasm 2.0
//   - `-0x10`/`-0x11` (func, extern) are Wasm 2.0's two, ungated, and `ref.null extern`
//     must decode with every gate off or the corpus breaks
//   - the other ten abstract forms are GC's
//
// The type-index gate sits *after* the negativity check, not before, and the order is
// load-bearing: `either` propagates `ErrFeatureDisabled` without backtracking (#86), so a
// gate check ahead of the discriminator would decline `ref.null extern` — whose byte is
// negative at s33 and belongs to the next branch — as a GC construct. Pinned by
// TestHeapTypeGatesFormsNotThePosition.
func (d *Decoder) decodeHeapType(r *reader) error {
	return either(r,
		func(r *reader) error {
			// `UseHT (typeuse s33 s)` — a type index. Negative values are not indices,
			// which is what sends the abstract forms to the next branch.
			v, err := r.sleb(33)
			if err != nil {
				return err
			}
			if v < 0 {
				return ErrMalformedTypeIndex
			}
			if !d.Features.GC {
				return featureErr("gc")
			}
			return nil
		},
		func(r *reader) error {
			form, err := r.sleb(7)
			if err != nil {
				return err
			}
			switch form {
			case -0x10, -0x11: // func, extern — Wasm 2.0, ungated
				return nil
			case -0x0C, -0x0D, -0x0E, -0x0F, // noexn, nofunc, noextern, none
				-0x12, -0x13, -0x14, -0x15, -0x16, -0x17: // any, eq, i31, struct, array, exn
				if !d.Features.GC {
					return featureErr("gc")
				}
				return nil
			}
			return fmt.Errorf("%w: %#02x", ErrMalformedHeapType, byte(form&0x7F))
		},
	)
}

// decodeLimits reads a limits flags byte and its min, plus max when present.
//
// The flags field is a single byte, not a LEB — which is the whole content of
// three suite vectors (binary.wast:632, :677, :686) that encode a *valid* flag
// value 1 as the two-byte LEB `\81\00` and expect "malformed limits flags". A
// decoder reading the flags with u32 would accept all three.
//
// min and max are read at **64 bits**, not 32, and the suite is unambiguous about
// it (grave #36). binary-leb128.wast:525 is a memory32 section whose min is a
// 10-byte LEB with unused bits set, and it wants "integer too large": ten bytes is
// legal *width* for a u64 and one byte too many for a u32, so a u32 read reports
// "integer representation too long" and scores the wrong string. The neighbouring
// :217 and :225 (11-byte fields) want "too long" and still get it, since 11 bytes
// overruns the u64 budget too — the two vectors bracket the width from both sides.
//
// The consequence is deliberate: a memory32 limit above 2^32 now decodes and is the
// *validator's* to reject ("memory size must be at most 65536 pages"), which is the
// correct layering. Reading the field narrowly to catch it here would be the decoder
// borrowing the validator's job and getting the malformed string wrong to do it.
//
// This is reader.u64's first production caller, closing #19's declared-and-tracked
// deferral by making it reachable rather than by allowlisting it.
// It returns the Limits it read rather than writing them to a Decoder field, and the
// asymmetry with decodeValType's out-parameter is a fact about the callers, not an
// inconsistency: `decodeValType` is handed to `either` and `decodeVec` as a
// `func(*reader) error` and cannot widen, while `decodeLimits` has three direct callers
// and no combinator. Where a return value is available it is preferable — a value that
// cannot outlive its read cannot be read stale.
func (d *Decoder) decodeLimits(r *reader) (Limits, error) {
	var lim Limits
	flags, err := r.byte()
	if err != nil {
		return lim, err
	}
	switch flags {
	case 0x00:
	case 0x01:
		lim.HasMax = true
	case 0x02, 0x03:
		if !d.Features.Threads {
			return lim, featureErr("threads")
		}
		lim.HasMax = flags == 0x03
	case 0x04, 0x05, 0x06, 0x07:
		if !d.Features.Memory64 {
			return lim, featureErr("memory64")
		}
		lim.HasMax = flags&0x01 != 0
	default:
		return lim, fmt.Errorf("%w: %#02x", ErrMalformedLimits, flags)
	}
	if lim.Min, err = r.u64(); err != nil {
		return lim, err
	}
	if lim.HasMax {
		if lim.Max, err = r.u64(); err != nil {
			return lim, err
		}
	}
	return lim, nil
}

// decodeTable reads one table — `table` (decode.ml:1049-1063).
//
// Two forms, and the second one is #51: function references adds a `0x40` prefix, a
// reserved zero byte, a tabletype, and a const-expr **initializer** —
// `(table 1 (ref func) (ref.func 0))` in text. Without this branch the 0x40 reached
// decodeRefType and came back `malformed reference type: 0x40`, the decoder rejecting a
// module the suite calls *valid*, seven times in elem.wast. A decoder that rejects valid
// modules is worse than one that misses an invalid one.
//
// **This is deliberately not an `either`, and the reason is a measurement.** The
// reference uses one, and copying that shape here would break the gate decline: `either`
// lets the *last* branch's error stand, so a `\40` with GC off would be judged by the
// plain branch and reported as `malformed reference type: 0x40` — the gate manufacturing
// malformedness for a module Wasm 3.0 defines, which is the exact defect #51 filed. The
// blocktype alternation solves the same problem by *ordering* (valtype last), and the
// first draft of this function tried to, with the gated branch first; that ordering is
// unreachable here, because the branch that must speak is the one the alternation is
// structured to discard.
//
// A switch is sound where blocktype's could not be, and that difference is measured, not
// assumed: over all 256 first bytes the two forms are **disjoint** — the plain branch
// accepts 12, the 0x40 form accepts 1, and *zero* bytes are accepted by both. `0x40` is
// not a legal reftype in any gate configuration. With no ambiguity there is nothing to
// backtrack for, so the alternation's only remaining effect would be to lose the error
// that matters. (The first version of this comment claimed the order also decides
// *extent*, since the forms consume different byte counts — that was invented reasoning,
// and the disjointness probe is what killed it. Extent cannot differ between branches
// that never both apply.)
func (d *Decoder) decodeTable(r *reader) error {
	tbl, err := d.decodeTableForm(r)
	if err != nil {
		return err
	}
	d.mod().Tables = append(d.mod().Tables, tbl)
	return nil
}

// decodeTableForm is decodeTable's grammar, returning what it read. Split so that
// decodeImport can read a table type *without* appending to m.Tables — an imported table
// occupies the table index space, but the module's own table section is a different
// population, and merging them here would make an import look like a definition.
func (d *Decoder) decodeTableForm(r *reader) (Table, error) {
	var tbl Table
	if b, ok := r.peek(); ok && b == 0x40 {
		// `expect 0x40 s ""; zero s; tabletype s; const s`.
		if _, err := r.byte(); err != nil {
			return tbl, err
		}
		if !d.Features.GC {
			// Function references, folded into the GC gate by decision 0008. Named
			// before the zero byte is read, so the decline describes the construct
			// rather than whatever byte happens to follow it.
			return tbl, featureErr("gc")
		}
		z, err := r.byte()
		if err != nil {
			return tbl, err
		}
		if z != 0x00 {
			return tbl, fmt.Errorf("%w: %#02x", ErrZeroByteExpected, z)
		}
		if tbl, err = d.decodeTableType(r); err != nil {
			return tbl, err
		}
		// The initializer, through the existing const-expr grammar — which is why this
		// is a small change rather than a new reader: #25's authority-derived table
		// already knows every const instruction's immediate widths.
		//
		// The initializer expression is **not retained** (#7): this form is GC-gated, so
		// no accepted module on the default board has one, and the interpreter has no GC
		// consumer to hand it to. Declared here rather than left to be noticed.
		return tbl, d.decodeConstExpr(r)
	}
	return d.decodeTableType(r)
}

// decodeTableType reads a tabletype: element type then limits (decode.ml:301-304).
//
// Split out of decodeTable because the 0x40 form needs it too, and because the reference
// has it as its own production. Both callers are in this file, so this is not the
// premature-sharing case decision 0006 warns about — the second consumer exists now.
func (d *Decoder) decodeTableType(r *reader) (Table, error) {
	var tbl Table
	if err := d.decodeRefType(r); err != nil {
		return tbl, err
	}
	tbl.ElemType = d.valType
	var err error
	tbl.Limits, err = d.decodeLimits(r)
	return tbl, err
}

func (d *Decoder) decodeMemory(r *reader) error {
	lim, err := d.decodeLimits(r)
	if err != nil {
		return err
	}
	d.mod().Memories = append(d.mod().Memories, Memory{Limits: lim})
	return nil
}

// decodeGlobalType reads a global's value type and mutability byte — `globaltype`
// (decode.ml:292-295).
//
// The mutability read is decodeMutability's, shared with `fieldtype`'s call site, because
// the reference shares it. The eight `malformed mutability` vectors in `global.wast` score
// this path and the one in `binary-gc.wast` scores the other; a second copy here would be
// green on both and drift on the next change to either.
func (d *Decoder) decodeGlobalType(r *reader) (ValType, bool, error) {
	if err := d.decodeValType(r); err != nil {
		return NoValType, false, err
	}
	vt := d.valType
	mut, err := d.decodeMutability(r)
	return vt, mut, err
}

// decodeImport reads module name, field name, and the kind-specific descriptor.
//
// **The names are copied, not aliased**, and this is the one place in the decoder where
// that is true. Everything else here holds `[]byte` views into the caller's image
// (Section.Payload says so explicitly), which is the in-place posture and correct for
// bytes the engine only ever compares. An import's names are different: they are the
// linker's keys, they outlive the decode, and a `string` conversion is the copy. A view
// would make a module's identity depend on the caller not reusing its buffer.
func (d *Decoder) decodeImport(r *reader) error {
	var im Import
	for i, dst := range [...]*string{&im.Module, &im.Name} {
		_ = i
		n, err := r.nameString()
		if err != nil {
			return err
		}
		*dst = n
	}
	kind, err := r.byte()
	if err != nil {
		return err
	}
	im.Kind = ExternKind(kind)
	switch kind {
	case 0x00: // func: a type index
		if im.Index, err = r.u32(); err != nil {
			return err
		}
	case 0x01:
		// Read through decodeTableForm, *not* decodeTable: an imported table must not
		// land in m.Tables. Both occupy the table index space, but the section holds
		// definitions and an import is not one — merging them would make the two
		// populations indistinguishable to every later consumer.
		if _, err = d.decodeTableForm(r); err != nil {
			return err
		}
	case 0x02:
		if _, err = d.decodeLimits(r); err != nil {
			return err
		}
	case 0x03:
		if _, _, err = d.decodeGlobalType(r); err != nil {
			return err
		}
	case 0x04: // tag
		if !d.Features.ExceptionHandling {
			return featureErr("exception handling")
		}
		if _, err = r.byte(); err != nil { // attribute
			return err
		}
		if _, err = r.u32(); err != nil { // type index
			return err
		}
	default:
		return fmt.Errorf("%w: %#02x", ErrMalformedImportKind, kind)
	}
	// The non-func descriptors are read and dropped (#7). Index is the type index for a
	// function import and unset for the others, which is what its comment says; a
	// consumer that needs an imported table's type is the reason to retain one, and no
	// such consumer exists yet.
	d.mod().Imports = append(d.mod().Imports, im)
	return nil
}

// decodeExport reads a name, a kind byte, and an index.
func (d *Decoder) decodeExport(r *reader) error {
	name, err := r.nameString()
	if err != nil {
		return err
	}
	kind, err := r.byte()
	if err != nil {
		return err
	}
	switch kind {
	case 0x00, 0x01, 0x02, 0x03: // func table memory global
	case 0x04: // tag
		if !d.Features.ExceptionHandling {
			return featureErr("exception handling")
		}
	default:
		return fmt.Errorf("%w: %#02x", ErrMalformedExportKind, kind)
	}
	idx, err := r.u32()
	if err != nil {
		return err
	}
	d.mod().Exports = append(d.mod().Exports, Export{Name: name, Kind: ExternKind(kind), Index: idx})
	return nil
}
