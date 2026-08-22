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
// The zero value means every gate off — a mechanism fact about the struct, always true, used
// wherever an explicit all-off decoder is wanted (fuzzing, const-verdict isolation, the const
// tests' own `&Decoder{}`). **Default policy is a different fact, and `DefaultFeatures` is its
// own name** — what a caller who does not choose gets, which is *not* required to be the zero
// value once a gate has flipped default-on (contract §9 G-1). The two facts were accidentally
// identical until #227's SIMD flip, the project's first: had "default policy" stayed spelled as
// `Features{}` throughout the codebase, flipping a gate would have meant either breaking the
// zero-value invariant every other caller relies on, or inverting a field's own polarity
// (`NoSIMD` instead of `SIMD`, false-means-on) — the deceptive-testimony shape this project has
// filed graves against elsewhere: a bool whose name lies about its semantics. `DefaultFeatures`
// keeps both facts true at once and turns every future flip into one line here.
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
	// The type bytes are named here and not in gatemap.go on that file's own rule —
	// "constructs that are not opcodes at all (limits flags, section ids, valtypes) stay where
	// they are checked, in sections.go" — and they are named at all because #395 found them
	// declared nowhere: 0008's mapping covers this gate's three opcodes and the two heap types
	// were in GC's arm with no entry anywhere saying so. An unstated scope is what let a wrong
	// one survive three PRs.
	ExceptionHandling bool // tag section (id 13), import/export kind 4; throw, throw_ref, try_table; the exn (-0x17) and noexn (-0x0c) heap types, so exnref/nullexnref (#395)
	SIMD              bool // v128 value type, including as a blocktype; the 0xfd region
	Threads           bool // shared limits flags (2, 3)
	Memory64          bool // 64-bit limits flags (4..7)

	// The four gates #48 found missing. A *tracked* proposal (contract §9 G-2) with no
	// bool here is worse than a gate that never fires, because the reflection-derived
	// lanes cannot exercise a gate that is not there to reflect over — the
	// forgotten-fifth-gate scenario existing in the wild, four times.
	GC          bool // the 0xfb region; ref.eq, the function-references five (0008); eight abstract heap types and the (ref ht)/(ref null ht) prefixes — ten of reftype's twelve, not twelve (#395)
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

// DefaultFeatures is what a caller who does not choose gets — v0's policy, not the struct's
// zero value (see Features's own doc comment for why the two are different facts). It starts
// equal to the zero value and diverges one field at a time as gates flip default-on, each such
// flip being its own two-principal-stamped decision (contract §9 G-1) with its own PR.
//
// **First divergence: SIMD, #227/ADR 0025.** G-1's own suite (`simd_*.wast`) measured
// pass=25158 fail=161 gated=0 **at the flip**, every fail attributed to #9's own deferred
// validator by the engine's error taxonomy — the named, self-retiring carve-out ADR 0025 added to
// G-1's text.
//
// Past tense on purpose, and re-measured because #464's reconciliation found this sentence
// asserting the flip-time figures in the present: the same 59 files now read **pass=25989 fail=0
// gated=0**, so **the carve-out's subject in this suite is empty**. It is *inert, not retired* —
// its retirement condition is #9 landing, and `ErrNotValidated` still has call sites throughout
// `internal/interp`. A flip-time measurement stated in the present tense is the foreclosing-words
// shape aimed at a number: still true of the moment it was taken, false of the tree it describes.
//
// **Second divergence: RelaxedSIMD.** G-1's own suite (the seven `*relaxed*.wast` files)
// measures pass=77 fail=0 unsupported=0 gated=0, identical on arm64/darwin and
// amd64/linux — so this flip satisfies G-1's *literal* reading and **does not invoke ADR
// 0025's carve-out at all**. That is stated rather than left implied: a carve-out cited
// where it is not load-bearing is a citation that will be believed the next time it is.
//
// The flip's board delta is 69 vectors converting `gated` → `pass` with a fail delta of
// zero, and `unsupported` is structurally unmoved — a relaxed vector is scored `gated`,
// never `unsupported`, so that column has no subject at a flip.
//
// What the flip *promises*, beyond passing vectors: ADR 0028 d1's guarantee that the
// relaxed lowerings are deterministic **and architecture-uniform** becomes a default-lane
// promise to a caller who asked for nothing. No vector can measure it — every `(either …)`
// alternative passes — so the instrument that holds it is
// `TestRelaxedLoweringChoicesArePinned` (#283), which pins all 32 choices by matched text.
func DefaultFeatures() Features {
	return Features{SIMD: true, RelaxedSIMD: true}
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

	// blockType and blockTypeIdx are the encoded blocktype the most recent `blocktype` read
	// accepted, on the Decoder for valType's reason — its alternation is an `either`. See
	// decodeBlockTypeValue for the encoding and for why the three forms cannot collide.
	//
	// **Two fields as of 0018's implementation**, not one: a bare-valtype blocktype whose
	// result is the indexed reference form (`(ref $t)`/`(ref null $t)`) needs a uint32 index
	// alongside the kind byte and null bit that still fit blockType, and Imm1 is where that
	// index rides in the retained Instr (module.go's BlockType doc). blockTypeIdx carries it
	// through decodeBlockTypeValue's third branch exactly as blockType carries the rest.
	blockType    uint64
	blockTypeIdx uint64

	// storageType is the StorageType the most recent successful `storagetype` read
	// accepted, on the Decoder for valType's exact reason: decodeStorageType is passed as a
	// `func(*reader) error` to `either` (its two branches are a valtype read or a packtype
	// read), so widening its signature would widen either's, and either's signature is the
	// shape of a backtracking alternation with no meaningful value to return for a branch
	// that failed. Written only immediately before a successful return, never on a path
	// that goes on to fail — valType's ordering discipline, restated for this field.
	storageType StorageType

	// fieldType is the FieldType the most recent successful `fieldtype` read accepted, on
	// the Decoder for the same reason storageType is: decodeFieldType is passed to
	// decodeVec (a struct's `vec(fieldtype)`) as a `func(*reader) error`, which cannot
	// return a value, while an array's single fieldtype is read by a direct call that
	// could. decodeFieldType is called both ways, so it stays error-only and writes here on
	// every accepting path, matching decodeValType's precedent for the identical shape.
	fieldType FieldType
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
// done" (docs/laws/graves-and-sweeps.md) — the alternative, a grammar that consumes `size` bytes and
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
		// Ranked by the structural layer (it is well-formed), accepted only by the gate —
		// §5's own "gates never manufacture malformedness" ruling, unaffected by the
		// payload grammar landing: gate-off still refuses the whole section rather than
		// reading into it, feature-named rather than a spoofed spec string.
		//
		// Payload grammar retained since #95 (0022 §3's own found prerequisite): before
		// this, section 13 accepted only by the gate with no grammar, `decodePayload`
		// returning `false` — well-formed by the tracked set, nothing kept, and
		// `Instance.tags` (0022) had nothing to build from.
		if !d.Features.ExceptionHandling {
			return false, featureErr("exception handling")
		}
		return true, d.decodeVec(r, d.decodeTag)
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
// No production call site keeps it as of #199's rung 1 (`try_table`'s catch indices were the
// last one, and decodeCatch now returns a Catch instead of discarding). Still referenced from
// `optable_agreement_test.go`, which uses it as a generic "read and drop one index" reader when
// probing the opcode table's shape against the reference — that use has no consumer of its own
// to arrive, so this function is not dead code (`deadcode`'s finding, if it fires, is a
// declared-and-tracked non-issue rather than a grave).
//
// **Five former production callers have dropped it as their consumers arrived**, and the list is
// kept current rather than describing the moment it was written: an explicit memory index
// (0015), `br_table`'s label vector (0016), an element segment's table index and element vector
// (0016), a subtype's declared supertype list (0019's own named gap — `decodeSubType` retains it
// for the declared-supertype walk, which as of 0042 is `internal/validate`'s
// `matchDeclaredSupertypes` and was `interp`'s deleted `sameFuncType`), and `try_table`'s
// catch-clause indices (#199).
// Each replacement reads the same `r.u32()` and appends the value instead of dropping it, so
// accept and reject behaviour is unchanged *by construction* — same reader, same width, same
// errors — which is what makes each retention invisible to the rejection vectors.
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
//
// # The group's extent is retained, and it is the type's identity rather than a grouping detail
//
// This function knew where each group started and ended and dropped both numbers, and that made
// **iso-recursive type equality uncomputable downstream** — the defect #343's slice hit. A
// deftype in the spec is `DefT (rectype, i)`: *the whole group plus the member's ordinal in it*,
// not the member's own comptype. So `(rec (type $a (func)) (type (struct)))` and
// `(rec (type (struct)) (type $b (func)))` hold two identical functypes that are **different
// types**, and so do `$a` and the `(func)` in a three-member group. Without the extent, the only
// relation a validator can compute is the *equi*-recursive one (bisimulation over indices), which
// is strictly coarser and accepts modules the spec rejects — the accept-direction failure no board
// sees by construction.
//
// `RecStart`/`RecLen` on each member is that extent, patched after the vector is read for
// `decodeSubType`'s reason: the members do not exist to be labelled until their own reads have
// appended them. A bare subtype is a **singleton group** and is labelled as one, which is not a
// convenience — decode.ml:276's non-`rec` arm produces `RecT [st]`, a one-member rectype, so the
// spec has no ungrouped types for this field to misreport.
func (d *Decoder) decodeRecType(r *reader) error {
	start := len(d.mod().Types)
	if b, ok := r.peek(); ok && b == -0x32&0x7F { // 0x4e — `rec`
		// A recursive type group is GC's, and the gate is checked before descending for
		// decodeRefType's reason: otherwise the member read reports the error and it
		// reports the wrong layer.
		if !d.Features.GC {
			return featureErr("gc")
		}
		r.skip(1) // the peeked discriminator — `skip 1 s` (decode.ml:275)
		if err := d.decodeVec(r, d.decodeSubType); err != nil {
			return err
		}
		d.labelRecGroup(start)
		return nil
	}
	if err := d.decodeSubType(r); err != nil {
		return err
	}
	d.labelRecGroup(start)
	return nil
}

// labelRecGroup stamps the extent of the rec group occupying Types[start:] onto every member.
//
// **Stamped from the slice's own length rather than from the declared vector count**, which is the
// difference between recording what was read and recording what was announced: `(rec)` is a legal
// empty group (`type-rec.wast:10`) and contributes no members, and a group whose count and
// contents disagreed would have failed the read before reaching here. Deriving the length from
// what is present cannot disagree with what is present.
func (d *Decoder) labelRecGroup(start int) {
	types := d.mod().Types
	n := uint32(len(types) - start)
	for i := start; i < len(types); i++ {
		types[i].RecStart = uint32(start)
		types[i].RecLen = n
	}
}

// decodeSubType reads a subtype: an optional supertype list, then a comptype
// (decode.ml:262-271).
//
// Both explicit forms carry `vec(typeuse u32)` — the declared supertypes — and differ only in
// finality: 0x50 is `NoFinal`, 0x4f is `Final`, and the no-wrapper fallthrough defaults to
// `Final, []` (decode.ml:271, `SubT (Final, [], comptype s)`). Peeked for decodeRecType's reason.
//
// **Retained as of 0019's own named gap, not discarded**: the supertype indices and the finality
// bit are read into locals and then patched onto the comptype `decodeCompType` appends, rather
// than being read into the comptype *before* it exists — `decodeCompType` is the function that
// knows which of its three arms fires and appends exactly one `CompType` on every accepting
// path, so patching its result is one write, not three (one per arm) duplicating the same
// fields. `Final` is patched even on the no-wrapper path, since its default (true) is not the
// zero value decodeCompType's own appends already carry — a bare `CompType{}` has `Final:
// false`, which would silently misreport every non-`sub` type as `NoFinal`.
func (d *Decoder) decodeSubType(r *reader) error {
	var supertypes []uint32
	final := true                                                      // decode.ml:271's default for the no-wrapper fallthrough — SubT (Final, [], ct)
	if b, ok := r.peek(); ok && (b == -0x30&0x7F || b == -0x31&0x7F) { // 0x50, 0x4f
		if !d.Features.GC {
			return featureErr("gc")
		}
		final = b == -0x31&0x7F // 0x4f is Final; 0x50 is NoFinal
		r.skip(1)               // `skip 1 s` (decode.ml:264, :268)
		// `vec (typeuse u32) s` — the supertypes, as plain type indices, following
		// `Func.TypeIndex`'s convention: index *validity* is #9's question, not this reader's.
		if err := d.decodeVec(r, func(r *reader) error {
			idx, err := r.u32()
			if err != nil {
				return err
			}
			supertypes = append(supertypes, idx)
			return nil
		}); err != nil {
			return err
		}
	}
	if err := d.decodeCompType(r); err != nil {
		return err
	}
	// decodeCompType has just appended exactly one CompType on this accepting path — its three
	// arms each end in one append and nothing else runs after — so the slot it occupies is the
	// type space's last index.
	last := &d.mod().Types[len(d.mod().Types)-1]
	last.Supertypes = supertypes
	last.Final = final
	return nil
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
		// Retained as of decision 0021: each accepted fieldtype is appended to fields
		// rather than discarded, the same shape decodeCompType's own functype branch
		// above already uses for Params/Results — one grammar, one traversal, writing
		// through the closure into a destination the loop does not otherwise see.
		var fields []FieldType
		if err := d.decodeVec(r, func(r *reader) error {
			if err := d.decodeFieldType(r); err != nil {
				return err
			}
			fields = append(fields, d.fieldType)
			return nil
		}); err != nil {
			return err
		}
		d.mod().Types = append(d.mod().Types, CompType{Kind: CompStruct, Fields: fields})
		return nil

	case -0x22: // 0x5e — arraytype: exactly one fieldtype
		if !d.Features.GC {
			return featureErr("gc")
		}
		if err := d.decodeFieldType(r); err != nil {
			return err
		}
		// Exactly one entry, per arraytype's own arity — decode.ml:257-258 reads a bare
		// fieldtype, not a vector, so there is no count to iterate and the field list
		// always has length 1.
		d.mod().Types = append(d.mod().Types, CompType{Kind: CompArray, Fields: []FieldType{d.fieldType}})
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
//
// **Writes d.fieldType on every accepting return, as of decision 0021** — the out-parameter
// discipline decodeValType's own field comment states, applied here because this function is
// called both through decodeVec (a struct's `vec(fieldtype)`, which needs a
// `func(*reader) error`) and directly (an array's bare fieldtype, which could return a
// value but must match the struct call site's signature to stay one function).
func (d *Decoder) decodeFieldType(r *reader) error {
	if err := d.decodeStorageType(r); err != nil {
		return err
	}
	mut, err := d.decodeMutability(r)
	if err != nil {
		return err
	}
	d.fieldType = FieldType{Storage: d.storageType, Mutable: mut}
	return nil
}

// decodeStorageType reads a storagetype: a valtype or a packed type (decode.ml:236-241).
//
// `either`, as the reference has it, and the ordering matters for the same reason
// decodeBlockType's does: the valtype branch runs first and its failure must not stand,
// since the cursor rewinds and the bytes get judged again as a packtype. The packtype
// branch is last, so its message — `malformed storage type` (:234) — is the one `either`
// returns for a byte that is neither.
//
// **Writes d.storageType on every accepting branch, as of decision 0021** — valType's own
// out-parameter discipline, restated here because decodeStorageType is itself passed to
// `either` and cannot widen its signature to return a value.
func (d *Decoder) decodeStorageType(r *reader) error {
	return either(r, func(r *reader) error {
		if err := d.decodeValType(r); err != nil {
			return err
		}
		d.storageType = StorageType{Val: d.valType}
		return nil
	}, func(r *reader) error {
		form, err := r.sleb(7)
		if err != nil {
			return err
		}
		if form != -0x08 && form != -0x09 { // i8, i16
			return fmt.Errorf("%w: %#02x", ErrMalformedStorageType, byte(form&0x7F))
		}
		width := byte(8)
		if form == -0x09 {
			width = 16
		}
		d.storageType = StorageType{Packed: true, Width: width}
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
		d.valType = ValType{kind: byte(form & 0x7F)}
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
// this engine's original two (0x70 funcref, 0x6F externref) are the Wasm 2.0 subset. Ten of
// the other twelve are GC's — eight abstract heap types and the two *parameterized* prefixes
// -0x1c (ref, non-null) and -0x1d (ref null), each of which is followed by a `heaptype`. **Two
// are the exception proposal's**, `-0x0c` (noexn) and `-0x17` (exn), and this sentence said
// twelve until #395: the ranges `-0x0c..-0x0f` and `-0x12..-0x17` are contiguous in the byte
// space and not in provenance, so a prose summary written from the switch's shape attributed by
// adjacency. *A comment that describes the code's grouping inherits the grouping's errors.* Reading them as `malformed reference type` was #51's
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
		// **Written as the reference's true abbreviation meaning: nullable** (grave #180,
		// correcting the non-null value #179 shipped). `funcref`/`externref` are the *(Null,
		// FuncHT)*/*(Null, ExternHT)* abbreviations (decode.ml's abbreviation table) — the
		// spec's own reading, not this engine's choice to make — so writing null:false here
		// silently declared a well-formed module's type wrong, an accept-direction defect
		// the default suite is structurally unlikely to exercise (§9 G-3): it collided
		// `funcref` with `(ref func)` (different spec types) and split `funcref` from
		// `(ref null func)` (the same spec type, spelled two ways). `FuncRef`/`ExternRef`
		// (module.go) moved to null:true in lockstep with this arm, so every existing
		// `t == FuncRef`-style comparison keeps compiling and returning the same answer as
		// before — the two were never independently observable from outside this package,
		// only their mutual agreement was, and that agreement is preserved by moving both
		// sides together rather than by holding either one fixed.
		d.valType = refKind(byte(form&0x7F), true)
		return nil

	case -0x0C, -0x17: // noexn, exn — `nullexnref` (0x74) and `exnref` (0x69)
		// **Exception handling's two, not GC's** (#395). These sat in the arm below for
		// three PRs, grouped by adjacency in the byte space rather than by proposal, and
		// the vendored proposal enumerates them per opcode
		// (`proposals/exception-handling/Exceptions.md:337-349`): *"The type `exnref` is
		// represented by the type opcode `-0x17`. … `noexn` is a new heap type with opcode
		// `-0x0c`"*. Decision 0008's mapping gave exception handling `0x08`, `0x0a`, `0x1f`
		// and never mapped these two anywhere; 0008's stated fold is function references
		// into GC, which does not reach a type the exception proposal introduces.
		//
		// The cost of the mis-attribution was not a wrong verdict on any vector — it was
		// that `gate:eh` did not admit its own proposal's value type, so every exception
		// witness in the tree had to open `GC` for a reason unrelated to what it tested.
		// **Neither board lane can see this in either direction**: the default lane has both
		// gates off and the all-on lane has both on, so no lane holds them apart. It was
		// found by writing #393's witness battery, and only its *accept* rows could have
		// found it — a refusal that should not happen is invisible to a row expecting a
		// refusal, which passes for the wrong reason.
		if !d.Features.ExceptionHandling {
			return featureErr("exception handling")
		}
		// Nullability as the arm below: the bare abstract form is the *(Null, ...)*
		// abbreviation, so `exnref` is `(ref null exn)` — which is exactly what the
		// proposal text quoted above says it is shorthand for.
		d.valType = refKind(byte(form&0x7F), true)
		return nil

	case -0x0D, -0x0E, -0x0F, // nofunc, noextern, none
		-0x12, -0x13, -0x14, -0x15, -0x16: // any, eq, i31, struct, array
		// GC's abstract heap types — **eight, since #395 moved exn and noexn to the arm
		// above.** Well-formed per Wasm 3.0, so the decline is feature-named (decision 0008
		// folds function references into the GC gate).
		if !d.Features.GC {
			return featureErr("gc")
		}
		// **Resolved, as of 0018's implementation** — every abstract heaptype `reftype`
		// names is representable now, so the real kind is written rather than the
		// NoValType sentinel this arm used to write for lack of anywhere to put the
		// answer. Unlike the Wasm 2.0 pair above, these eight have no pre-existing byte
		// behavior to preserve, so they get the reference's actual nullability:
		// `reftype`'s bare abstract forms are the *(Null, ...)* abbreviations —
		// `anyref` means `(Null, AnyHT)`, not `(NoNull, AnyHT)` — so null=true.
		d.valType = refKind(byte(form&0x7F), true)
		return nil

	case -0x1C, -0x1D: // (ref ht), (ref null ht)
		// The parameterized forms, each followed by a heaptype. The gate is checked
		// *before* descending, because the heaptype read would otherwise be the thing
		// that reports the error and it would report the wrong layer.
		//
		// **This prefix stays GC's after #395, and that is the boundary of what #395 fixed.**
		// The prefix is function-references' grammar (folded into GC by 0008) whatever
		// heaptype follows it, so `exnref` — the bare shorthand — needs only the exception
		// gate, while the spelled-out `(ref exn)` needs GC as well. The exception proposal is
		// layered on typed references and borrows this prefix rather than introducing it, so
		// the split is the gate boundary telling the truth about two proposals rather than a
		// leftover: the repair is complete for the shorthand and *deliberately partial* for
		// the parameterized form. Stated because "the eh gate admits its own grammar" is now
		// true of exactly the forms this proposal defines and no more.
		if !d.Features.GC {
			return featureErr("gc")
		}
		if err := d.decodeHeapType(r); err != nil {
			return err
		}
		// **Resolved, as of 0018's implementation.** decodeHeapType already wrote
		// d.valType with the heaptype's kind/idx; this branch's own job is only to
		// overwrite its nullability, since `heaptype` itself has no null bit and
		// `-0x1c`/`-0x1d` is exactly where the reftype grammar supplies one — the
		// prefix that told us whether this is `(ref ht)` or `(ref null ht)`.
		d.valType.null = form == -0x1D
		return nil
	}
	return fmt.Errorf("%w: %#02x", ErrMalformedRefType, byte(form&0x7F))
}

// decodeHeapType reads a heap type — `heaptype` (decode.ml:178-198).
//
// An `either` alternation in the reference: a type *index* (s33, so a plain funcidx-style
// number) or one of the **twelve** abstract forms (decode.ml:179-200, counted: `-0x0c` through
// `-0x17` with no gaps). This said *eleven* from 0018's implementation until 0027 needed the
// number for real, and the miscount is the ordinary drift of a figure written once and never
// re-derived — the switch below has always had all twelve, and `-0x17`/exn is the one the prose
// lost. Nothing depended on the wrong number, which is exactly why nothing caught it. Written as one here for the reason
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
//   - `-0x0c`/`-0x17` (noexn, exn) are the **exception proposal's** and gate on
//     `ExceptionHandling` — #395, which found them in GC's arm for the three PRs since 0018's
//     implementation; the grouping was by byte adjacency and the authority is per-opcode prose
//   - the other eight abstract forms are GC's
//
// The type-index gate sits *after* the negativity check, not before, and the order is
// load-bearing: `either` propagates `ErrFeatureDisabled` without backtracking (#86), so a
// gate check ahead of the discriminator would decline `ref.null extern` — whose byte is
// negative at s33 and belongs to the next branch — as a GC construct. Pinned by
// TestHeapTypeGatesFormsNotThePosition.
//
// **Writes d.valType on every accepting branch, as of 0018's implementation** — a heaptype
// (not a reftype) has no nullability of its own, so the resolved ValType here always has
// null false; a caller that reads it as a reftype's parameter (decodeRefType's
// `-0x1c`/`-0x1d` branch) supplies the bit that heaptype's own grammar does not carry, per
// the same reasoning `immHeapType`'s doc comment already gives for why `ref.null`'s
// immediate has no nullability of its own. A caller that does not need a resolved type
// (`immHeapType`'s ref.null/ref.test/ref.cast arms, which retain nothing per #7) simply
// leaves the write unread — safe by the same out-parameter discipline decodeValType's field
// comment states: written only immediately before a successful return.
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
			d.valType = RefType(uint32(v), false)
			return nil
		},
		func(r *reader) error {
			form, err := r.sleb(7)
			if err != nil {
				return err
			}
			switch form {
			case -0x10, -0x11: // func, extern — Wasm 2.0, ungated
				d.valType = refKind(byte(form&0x7F), false)
				return nil
			case -0x0C, -0x17: // noexn, exn — the exception proposal's (#395)
				if !d.Features.ExceptionHandling {
					return featureErr("exception handling")
				}
				d.valType = refKind(byte(form&0x7F), false)
				return nil
			case -0x0D, -0x0E, -0x0F, // nofunc, noextern, none
				-0x12, -0x13, -0x14, -0x15, -0x16: // any, eq, i31, struct, array
				if !d.Features.GC {
					return featureErr("gc")
				}
				d.valType = refKind(byte(form&0x7F), false)
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
		// Retained as of 0015: the address width is only knowable from this byte, and the
		// interpreter's bounds check needs it. Set here rather than derived at use time
		// because by then the flags are gone.
		lim.Addr64 = true
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

// decodeTableForm is decodeTable's grammar, returning what it read — `table` (decode.ml:1049-1064),
// which is the `0x40` form or a bare tabletype whose initializer the reference *synthesizes*.
//
// Split from decodeTable so the result can be inspected before it is appended, and **not** shared
// with decodeImport, which is grave #420. The comment here used to say the split existed so
// "decodeImport can read a table type without appending to m.Tables", a split about the
// *population* — true as far as it went, and it silently asserted that the two callers wanted the
// same grammar. They do not: an import's descriptor is `tabletype` (`externtype`'s `0x01` arm,
// decode.ml:309), a production with no `0x40` arm, so reading an import through here accepted
// `0x40 0x00 tabletype const` in an import descriptor where the reference answers `malformed
// reference type`. Measured on the all-gates-on lane, with no corpus vector in either direction.
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
		tt, err := d.decodeTableType(r)
		if err != nil {
			return tbl, err
		}
		tbl.ElemType, tbl.Limits = tt.ElemType, tt.Limits
		// The initializer, through the existing const-expr grammar — which is why this
		// is a small change rather than a new reader: #25's authority-derived table
		// already knows every const instruction's immediate widths.
		//
		// **Retained as of #419**, where this said "not retained (#7): this form is GC-gated, so
		// no accepted module on the default board has one, and the interpreter has no GC consumer
		// to hand it to." Both halves of that were true and both expired together: the validator's
		// `check_table` rule and the interpreter's `init_table` are the consumers, and the *plain*
		// form's synthesized initializer below is what puts a default-lane module on this field.
		if tbl.Init, tbl.InitCasts, err = d.decodeConstExpr(r); err != nil {
			return tbl, err
		}
		return tbl, nil
	}
	tt, err := d.decodeTableType(r)
	if err != nil {
		return tbl, err
	}
	tbl.ElemType, tbl.Limits = tt.ElemType, tt.Limits
	// `let c = [RefNull ht @@ at] @@ at` (decode.ml:1058-1063): the bare form is **sugar**, not a
	// table without an initializer. Synthesizing it here rather than letting each consumer read
	// "empty means null" is what keeps the two wire forms from being two cases anywhere else —
	// `check_table` runs one rule and `init_table` evaluates one expression, whichever form the
	// module was written in.
	tbl.Init, tbl.InitCasts = synthesizedRefNull(tbl.ElemType)
	return tbl, nil
}

// synthesizedRefNull builds `[RefNull ht; end]` for a tabletype's own heap type — the initializer
// `decode.ml:1061` invents for the plain table form.
//
// **The cast row is the whole reason this is a function rather than two literals.** `ref.null`'s
// heaptype claims no immediate word (`immHeapType` stages into `heaps` and files through
// `castTypes`), so an `Instr{Op: opRefNull}` on its own carries *no* record of which null it is —
// `ref.null func` and `ref.null extern` are the identical struct. The side-table row is where the
// type lives, and a synthesized initializer that omitted it would type as `ref.null` of nothing at
// all: #361's shape, one construct over.
//
// `WithNull(true)` and not the reftype as written, because the reference destructures the heap type
// out of it (`TableT (_, _at, (_, ht))`) and `valid.ml:714-716` types `ref.null ht` as
// `RefT (Null, ht)`. So a `(table 1 (ref func) …)` synthesizes `ref.null func`, whose type is
// `funcref` — nullable, where the table's element type is not. That is the reference's own answer
// and it is why the plain form of such a table is invalid rather than merely unusual.
// The results are named because they are the two halves of one expression and a caller reading
// `([]Instr, map[int][]ValType)` cannot tell which map keys what: `casts` is `Table.InitCasts`'
// shape, keyed by instruction index within `init`.
func synthesizedRefNull(elem ValType) (init []Instr, casts map[int][]ValType) {
	return []Instr{{Op: opRefNull}, {Op: opEnd}},
		map[int][]ValType{0: {elem.WithNull(true)}}
}

// decodeTableType reads a tabletype: element type then limits (decode.ml:301-304).
//
// Split out of decodeTable because the 0x40 form needs it too, and because the reference
// has it as its own production. Both callers are in this file, so this is not the
// premature-sharing case decision 0006 warns about — the second consumer exists now.
//
// **It returns a `TableType`, and that return type is the fix for grave #420.** Two callers wanted
// two different productions from one function while the return type could not tell them apart; a
// `tabletype` reader that returns a `tabletype` cannot be mistaken for the `table` reader, and
// `decodeImport` is now the caller that says so.
func (d *Decoder) decodeTableType(r *reader) (TableType, error) {
	var tbl TableType
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
		// **`decodeTableType`, which is `tabletype` — not `decodeTableForm`, which is `table`.**
		// `externtype`'s table arm is `ExternTableT (tabletype s)` (decode.ml:309), and `tabletype`
		// has no `0x40` arm: the initializer form belongs to the table *section*'s production. This
		// read `decodeTableForm` and therefore accepted `0x40 0x00 tabletype const` in an import
		// descriptor, which the reference answers `malformed reference type` — grave #420, and the
		// reason its diagnosis needed the reference rather than the board (no vector, either lane).
		//
		// It is also still true that an imported table must not land in `m.Tables`: both occupy the
		// table index space, but the section holds definitions and an import is not one. That was
		// this comment's whole content and it was the *weaker* of the two reasons.
		if im.Table, err = d.decodeTableType(r); err != nil {
			return err
		}
	case 0x02:
		if im.Memory.Limits, err = d.decodeLimits(r); err != nil {
			return err
		}
	case 0x03:
		if im.GlobalType, im.GlobalMutable, err = d.decodeGlobalType(r); err != nil {
			return err
		}
	case 0x04: // tag
		if !d.Features.ExceptionHandling {
			return featureErr("exception handling")
		}
		// **Retained in Index, not a new field, closing #204** — `Import`'s own stated pattern
		// for a func import applies unchanged: a tag's descriptor *is* a type index into the
		// module's own type space (`tagtype = TagT of typeuse`, the same grammar `decodeTag`
		// reads for a *defined* tag), so no separate field is needed to carry it. Before this,
		// both the attribute byte and the type index were decoded and discarded — 0022 §3's
		// `Instance.link` tag-import placement needs the declared type to compare against a
		// supplier's actual one, and it had nothing to read. The attribute byte is checked,
		// not skipped — `decodeTag`'s own reasoning, `ErrZeroByteExpected` rather than a
		// silently-accepted reserved value.
		z, err := r.byte()
		if err != nil {
			return err
		}
		if z != 0 {
			return fmt.Errorf("%w: %#02x", ErrZeroByteExpected, z)
		}
		if im.Index, err = r.u32(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: %#02x", ErrMalformedImportKind, kind)
	}
	// The non-func descriptors are now retained (im.Table/im.Memory/im.GlobalType/
	// im.GlobalMutable, #164) rather than read and dropped — Instance.link is the consumer
	// that needed them, to compare an import's *type* against a supplied extern and not
	// only its kind.
	d.mod().Imports = append(d.mod().Imports, im)
	return nil
}

// decodeTag reads one tag section entry: the fixed zero attribute byte, then a type index —
// `tagtype s = zero s; TagT (typeuse idx s)` (decode.ml:288-290).
//
// **The zero byte is checked, not skipped**, via the same `ErrZeroByteExpected` sentinel every
// other reserved byte in this decoder already reports through — a byte the reference never
// reads back is still part of the grammar it validated, so a nonzero attribute byte is a
// malformed module rather than a forward-compatible extension this decoder happens not to use.
func (d *Decoder) decodeTag(r *reader) error {
	z, err := r.byte()
	if err != nil {
		return err
	}
	if z != 0 {
		return fmt.Errorf("%w: %#02x", ErrZeroByteExpected, z)
	}
	idx, err := r.u32()
	if err != nil {
		return err
	}
	d.mod().Tags = append(d.mod().Tags, Tag{TypeIndex: idx})
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
