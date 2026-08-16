package binary

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
)

// The instruction grammar, driven by the generated table (0007).
//
// # Why this file replaced a narrow accept set
//
// decodeConstExpr used to hold `constExprOps`, an eight-entry map of the
// const-legal opcodes, and it rejected everything else with a single error that
// deliberately named no spec string. The reason was stated at the time and was
// correct: two cases sat behind that branch — a byte that is no opcode at all
// (*malformed*, `illegal opcode`) and a real opcode that is simply not constant
// (*invalid*, `constant expression required`) — and the reader could not tell them
// apart, so claiming either would be a verdict it had not computed.
//
// The generated table answers the existence question over the whole space, so the
// verdict is now computed. #43 pinned the partition that made this a work list
// rather than a hope: over the 256 single-byte opcodes, 38 absent, 3 escapes, 21
// explicitly illegal, 2 reason arms, 192 plain instructions.
//
// # The layering, and the one thing that surprised
//
// The reference does not check const-ness in the decoder at all: `const s = at
// instr_block s; end_ s` (decode.ml:983) is the *full* instruction grammar. So
// const-ness is a validation fact, and the shape that follows is not "reject the
// first non-const instruction" but **read the whole expression, then report**.
//
// That distinction is load-bearing rather than stylistic, and binary.wast:112 is the
// vector that proves it. Its global section ends `\41\00` with no END, and the next
// byte is the code section's id `\0a` — which *is* an opcode, `throw_ref`. An aborting
// reader stops there and reports a const violation. The reference reads on: throw_ref
// takes no immediates, the following `\04` is `if`, whose blocktype eats `\01` and
// whose block eats `\00` and whose `end_` eats `\0b`, and the expression then runs
// off the end of the image — so the verdict is `unexpected end of section or
// function`, which is what the suite asks for. A malformed verdict from a lower layer
// must be allowed to win, and it can only win if the grammar is allowed to finish.
//
// So the read defers: `instr` records the first non-const instruction in `instrCtx.nonConst`
// and `constExprBody` returns it only if the grammar completed. *An invalid verdict that
// pre-empts a malformed one is reporting the wrong layer's answer* — the same error as an
// error from the wrong layer (#36), pointed the other way.
//
// Those two names are named because the previous version of this sentence said "constWalk
// defers" and no function of that name has ever existed in this package — a citation to a
// symbol is as checkable as a `.wast:N`, and this one resolved to nothing through three
// PRs (#109 swept it).
//
// Grave #116, and it is the one of that family with work still open. Identifiers in prose
// were left unchecked on the criterion *convention until first drift*; `constWalk` is the
// first measured drift — not renamed or moved but **fiction from the first keystroke** — so
// the criterion has fired and the resolving check `TestEveryCitedTestNameResolves` performs
// for test names is owed to identifiers generally. Until it exists, this class is convention
// again, which is the state that produced this comment.
//
// # What the table drives and what it cannot
//
// Dispatch, existence, illegality, escape, and immediate *shape* are all read from the
// table. Four arms cannot be: `block`, `loop`, `if`, `try_table` recurse through the
// instruction grammar and then consume an END, and `if` reads a second block only when
// an ELSE is peeked. `immBlock` in a row's immediates is the marker for exactly that
// set, and TestStructuralArmsAreExactlyTheBlockRows asserts the hand-written set and
// the table's `immBlock` rows are the same set — so a fifth structural arm upstream is
// a build failure rather than a row read with the wrong shape.

// constOps is the const-legal subset, and it is now the *only* thing this file knows
// that the table does not.
//
// It shrank from a table of opcodes-and-immediate-shapes to a set of bytes, which is
// the dissolution: the immediate shapes moved to the authority-derived table and only
// the const-legality predicate — the fact the reference does not encode — stayed
// behind. It is a set rather than a map because there is nothing left to associate.
//
// Closed by the *grammar*, not by what the suite exercises: the spec's constant
// expression is `t.const`, `ref.null`, `ref.func`, and `global.get` of an immutable
// import, terminated by END. WasmGC (`struct.new`) widens it and arrives with its gate.
//
// **Extended-const widens it too, and it is a gate now** (#109, stamped by Scott). The sentence
// that used to stand here claimed extended-const "arrives with its gate" while no such gate
// existed: `i32.add` in a constexpr was rejected outright with `constant expression required`, on
// nine modules the suite requires accepted (`data.wast:178`, `elem.wast:1057`, `global.wast:3`,
// and six more). The claim read as *declared and tracked* and licensed the omission, which is the
// defect-stated-as-the-rule shape — review verifies code against claims, and this claim was
// false. Found by the #67 cross-check corpus, because every one of the 4162 green vectors is a
// rejection and no board can see a decoder that wrongly rejects (contract §9 G-3).
//
// The ruling also amended G-2: extended-const is Wasm 3.0 core and the parenthetical that omitted
// it is why the false comment was believable. See `extendedConstOps` for why the gate cannot be a
// `gatedOpcodes` entry, and `constLegal` for where it is checked.
var constOps = map[byte]bool{
	0x41: true, // i32.const
	0x42: true, // i64.const
	0x43: true, // f32.const
	0x44: true, // f64.const
	0x23: true, // global.get
	0xD0: true, // ref.null
	0xD2: true, // ref.func
}

// extendedConstOps is the set extended-const adds to constOps when its gate is on.
//
// A **separate map rather than six more rows in `constOps`**, because the two differ in exactly
// the way the gates doctrine cares about: `constOps`' members are const-legal unconditionally,
// and these six are const-legal only under a feature. Merging them would need a per-row gate
// field, which is a second authority inside one table — the thing 0008 kept out of `optable.go`.
//
// The bytes are read from the generated table's own mnemonics rather than typed from the
// proposal: `i32.add` is 0x6a (optable.go:179), `i32.sub` 0x6b, `i32.mul` 0x6c, `i64.add` 0x7c
// (:197), `i64.sub` 0x7d, `i64.mul` 0x7e. The proposal document lists the six by *name*
// (`proposals/extended-const/Overview.md:41-46`) and names are not encodings; the join between
// them is `TestExtendedConstOpsAreTheProposalsSix`, which checks these keys against the table's
// mnemonics so a transcription slip is a build failure rather than a wrong verdict on one opcode.
//
// **`i32.const` is not here and the near-miss is the reason to say so**: `0x41` versus `0x6a` is
// one bucket of the board apart, and a slip that put a const opcode in this map would make it
// gate-dependent — breaking every MVP module — while a slip in the other direction would make an
// extended-const opcode unconditionally legal, which no vector can see.
var extendedConstOps = map[byte]bool{
	0x6a: true, // i32.add
	0x6b: true, // i32.sub
	0x6c: true, // i32.mul
	0x7c: true, // i64.add
	0x7d: true, // i64.sub
	0x7e: true, // i64.mul
}

// prefixRegions is every region of the generated table, keyed by prefix byte with 0x00
// meaning "no prefix".
//
// It lived in the agreement test until the decoder needed it, which is the right
// direction of travel: a walk the test does over "every region" and a dispatch the
// decoder does over "every region" must be the same set, and the way to guarantee that
// is one definition. TestPrefixRegionsCoverTheTable checks it against the table's own
// `escape` rows in both directions, so a fourth prefix arriving upstream is a build
// failure rather than an uncovered region (*derive the domain, never enumerate it*).
var prefixRegions = map[byte]map[uint32]opInfo{
	0x00: opTable,
	0xfb: opTableFB,
	0xfc: opTableFC,
	0xfd: opTableFD,
}

// prefixRegion looks up a sub-table. The 0x00 entry is excluded deliberately: it is the
// single-byte table, not a prefix region, and a dispatch that found it there would read
// a sub-opcode against the wrong space.
func prefixRegion(prefix byte) (map[uint32]opInfo, bool) {
	if prefix == 0x00 {
		return nil, false
	}
	region, ok := prefixRegions[prefix]
	return region, ok
}

// opEnd is END (0x0B), and opElse is ELSE (0x05): the two bytes `instr_block'` stops
// on without consuming (decode.ml:969).
//
// They are delimiters, not instructions, at every position this file reads from — which
// is why the table's two `reason` rows are unreachable. See ErrMisplacedOpcode.
const (
	opEnd  = 0x0B
	opElse = 0x05
)

// instrCtx carries the state one instruction-sequence read needs beyond the cursor.
//
// constOnly is a *reporting* mode, not a grammar mode: the grammar is identical either
// way, and all the flag does is arm the deferred verdict. See the file comment.
type instrCtx struct {
	d         *Decoder
	constOnly bool
	// nonConst is the first non-const instruction seen, or -1. Recorded rather than
	// returned, so a malformed verdict from further along still wins.
	nonConst int
	// declined is the first gated construct met with its gate off, or nil.
	//
	// Deferred for the same reason nonConst is, and binary.wast:112 is the vector that
	// proves it rather than a symmetry argument: that global initialiser ends `\41\00`
	// with no END and the next byte is the code section's id `\0a`, which *is*
	// throw_ref. A reader that returns ErrFeatureDisabled on sight reports a gate
	// decline for a module the suite calls malformed — the wrong layer's answer, and
	// worse than the const case, because it also parks the vector in `gated` where
	// TestGatedVectors demands an allowlist entry for a decline that is pure artifact.
	//
	// So: malformed wins over both deferred verdicts; then the feature decline; then the
	// const verdict. That last order is decided in 0008 and not by the reference, which
	// has neither — the engine's configuration is a more fundamental "no" than a
	// validation rule about a construct it does not implement.
	declined error

	// out is the retained instruction sequence, or nil when this read is recognizing
	// only.
	//
	// **Nil is a real mode, not an unset field.** Most const-expression sites are read
	// to prove their bytes are well-formed and have no consumer for the result — an
	// element segment's offset, a GC table initializer — and allocating a slice for each
	// would be paying the rewrite's build cost for a program nobody runs. So `emit`
	// checks for nil and the retaining call sites opt in. That check is also what keeps
	// this change off the 4162 rejection vectors' hot path.
	out *[]Instr

	// imm0, imm1 stage the current instruction's immediates while `imms` reads them.
	//
	// On the ctx rather than threaded through `imm`'s signature, for `decodeValType`'s
	// reason one layer up: `imm` is a switch over a vocabulary whose arms delegate to
	// productions with fixed shapes (`decodeVec`, `either`, `decodeHeapType`), and
	// widening it to return a value would mean each arm inventing one. Staging keeps the
	// arms as they are — the shape 4162 vectors are proven against — and lets the ones
	// that carry data say so.
	//
	// Reset by `emit`, not by `imms`: an arm that writes nothing leaves zero rather than
	// the previous instruction's immediate, which is the stale-field failure
	// decodeValType's out-parameter comment describes.
	imm0, imm1 uint64

	// immN counts the immediates staged for the current instruction, so `emit` can tell
	// "wrote 0" from "wrote 0 immediates". Nothing reads it as a value yet; it exists
	// because the alternative is a consumer unable to distinguish an `i32.const 0` from
	// an opcode with no immediates, which is a distinction the interpreter needs the
	// moment it dispatches on more than one arm.
	immN int

	// labels stages the current instruction's unbounded immediate — `br_table`'s label
	// vector — for the same reason imm0/imm1 are staged: the arm that reads it does not know
	// the instruction's index, and `emit` does.
	//
	// **Distinguished from nil by `hasLabels`, not by length.** A `br_table` with zero labels
	// is legal and means every index takes the default, so `len(labels) == 0` cannot serve as
	// "no vector was read" — the same distinction `LabelVector`'s two results carry through to
	// consumers. Conflating them would execute a legal instruction as though its immediate
	// were absent.
	labels    []uint32
	hasLabels bool

	// labelsOut is where emit files a staged vector, or nil when this read is recognizing
	// only. Parallel to `out` and nil in exactly the same cases: a const-expression site read
	// to prove its bytes are well-formed has no consumer for a label vector either, and
	// `br_table` is not const-legal in any case.
	labelsOut *map[int][]uint32

	// catches stages the current instruction's unbounded immediate — `try_table`'s
	// handler-clause vector — on the same discipline `labels` stages `br_table`'s: the arm
	// that reads it does not know the instruction's index, and `emit` does.
	//
	// **Distinguished from nil by `hasCatches`, not by length.** A `try_table` with zero
	// catch clauses is legal and means every exception falls through uncaught, so
	// `len(catches) == 0` cannot serve as "no vector was read" — `Catches.CatchVector`'s
	// two-result comment carries this through to consumers, mirroring `LabelVector`'s.
	catches    []Catch
	hasCatches bool

	// catchesOut is where emit files a staged catch-clause vector, or nil when this read is
	// recognizing only. Parallel to `labelsOut` and nil in exactly the same cases:
	// `try_table` is not const-legal in any case, so every const-expression call site leaves
	// this nil along with labelsOut.
	catchesOut *Catches

	// heaps stages the heaptypes read by the current instruction, in wire order.
	//
	// **Deliberately not a reftype and deliberately not filed by `imms`.** The wire holds a bare
	// *heaptype* everywhere this arm runs (decode.ml:603 for `ref.null`, :636-650 for the cast
	// family), so `decodeHeapType` always yields `null: false` and the nullability of the type
	// actually being tested comes from somewhere else entirely: the opcode for `fb 14`-`fb 17`,
	// the flags byte for `fb 18`/`fb 19`. An arm inside `imms` cannot see either — it is a switch
	// over an immediate *kind*, one opcode removed from the opcode. So this slot carries what the
	// grammar read, and `castTypes` is where the opcode's contribution is applied.
	//
	// No `hasHeaps`: the empty-versus-absent distinction that `hasLabels` exists for has no case
	// here, since every instruction reading a heaptype reads at least one. What answers "was
	// anything retained" is `hasCasts`, downstream of the interpretation step.
	heaps []ValType

	// casts stages the reference types the cast family tests against — the interpreted form of
	// `heaps`, nullability applied — and `hasCasts` distinguishes "filed" from "empty", on the
	// same discipline `hasLabels` follows.
	//
	// Set by `castTypes` rather than by an `imms` arm, and only for the six cast-family opcodes:
	// `ref.null` reads a heaptype too and files nothing, because 0027 decision 4 is that a null
	// keeps no heaptype (the reference's `NullRef` takes no argument and `type_of_ref` maps every
	// null to a single universal `BotHT`). A `ref.null` entry here would be a retained fact with
	// no consumer, which is the shape `immVecValType`'s comment declines for the same reason.
	casts    []ValType
	hasCasts bool

	// castsOut is where emit files a staged cast-type vector, or nil when this read is
	// recognizing only — parallel to `labelsOut`/`catchesOut` and nil in exactly the same cases.
	// Unlike those two the const-expression case is not vacuous by grammar: `ref.null` is
	// const-legal and does read a heaptype. It is vacuous by the rule above instead — a
	// const-expression read never reaches `castTypes`, because no cast-family opcode is
	// const-legal.
	castsOut *map[int][]ValType

	// selects stages `select`'s result-type annotation, and `hasSelects` distinguishes "filed"
	// from "empty" on the discipline `hasLabels` sets.
	//
	// **This is the case that discipline was written for, and it is not hypothetical here.** A
	// `select (result)` — arity 0 — is a legal *encoding* whose vector is empty, and the validator's
	// job is to reject it by the reference's arity rule (`valid.ml:443`, `select.wast:368`). With
	// length as the discriminator that vector would be indistinguishable from opcode `0x1B`, which
	// carries no annotation at all and is legal, so the one vector this retention exists to convert
	// would read as the form that needs no checking.
	selects    []ValType
	hasSelects bool

	// selectsOut is where emit files a staged annotation, or nil when this read is recognizing
	// only — parallel to the three above, and vacuous in the const-expression case by grammar:
	// `select` is not const-legal.
	selectsOut *map[int][]ValType
}

// emit appends one decoded instruction to the retained sequence and clears the staging
// fields.
//
// Called on the *accepting* path only — after the opcode's malformed verdicts and after
// its immediates have been read without error. An instruction emitted before its
// immediates were read would be an instruction the module might not contain.
func (c *instrCtx) emit(prefix byte, op uint32) {
	if c.out != nil {
		// The index is taken *before* the append, which is what makes it the index of the
		// instruction being emitted rather than of the next one. A staged label vector is
		// filed under it, so the key and the instruction cannot disagree: both come from the
		// same append.
		idx := len(*c.out)
		*c.out = append(*c.out, Instr{Op: op, Prefix: prefix, Imm0: c.imm0, Imm1: c.imm1})
		if c.hasLabels && c.labelsOut != nil {
			if *c.labelsOut == nil {
				*c.labelsOut = map[int][]uint32{}
			}
			// A nil vector is stored as an empty non-nil one, so `LabelVector`'s second
			// result stays the only thing that says "no vector" — see its comment.
			v := c.labels
			if v == nil {
				v = []uint32{}
			}
			(*c.labelsOut)[idx] = v
		}
		if c.hasCatches && c.catchesOut != nil {
			if *c.catchesOut == nil {
				*c.catchesOut = Catches{}
			}
			// A nil vector is stored as an empty non-nil one, matching Labels' rule and for
			// the same reason: CatchVector's second result is what says "no vector",
			// never len(x) == 0.
			cv := c.catches
			if cv == nil {
				cv = []Catch{}
			}
			(*c.catchesOut)[idx] = cv
		}
		if c.hasCasts && c.castsOut != nil {
			if *c.castsOut == nil {
				*c.castsOut = map[int][]ValType{}
			}
			// A nil vector is stored as an empty non-nil one, matching Labels' and Catches'
			// rule: `CastTypes`' second result is what says "no vector", never len(x) == 0.
			tv := c.casts
			if tv == nil {
				tv = []ValType{}
			}
			(*c.castsOut)[idx] = tv
		}
		if c.hasSelects && c.selectsOut != nil {
			if *c.selectsOut == nil {
				*c.selectsOut = map[int][]ValType{}
			}
			// A nil vector is stored as an empty non-nil one, matching the three above. The
			// case is *reachable* here rather than defensive — `select (result)` stages no
			// element — and `SelectTypes`' second result is what says "no annotation".
			sv := c.selects
			if sv == nil {
				sv = []ValType{}
			}
			(*c.selectsOut)[idx] = sv
		}
	}
	c.imm0, c.imm1, c.immN = 0, 0, 0
	c.labels, c.hasLabels = nil, false
	c.catches, c.hasCatches = nil, false
	c.selects, c.hasSelects = nil, false
	// `heaps` is cleared here and not by `castTypes`, so an instruction that reads a heaptype
	// and files nothing (`ref.null`) cannot leave one for the next instruction to inherit —
	// the stale-field failure this whole staging area is documented against.
	c.heaps, c.casts, c.hasCasts = nil, nil, false
}

// stage records one immediate for the instruction being read, in field order.
//
// A full 64-bit word per call. The narrow immediates that have to share a word do so
// through stageLaneIdx below, never by this switch guessing at widths.
//
// The two-slot cursor is not a bound on how many *values* an arm may carry — see
// stageLaneIdx — but it is a hard bound on how many words it may claim, and reaching a
// third would drop one. TestInstrImmediateWidthCoversTheTable is the control, scoped to
// every row of every region: it sums each arm's committed bits through the same width
// table this reader uses and fails if any row exceeds Instr's 128.
func (c *instrCtx) stage(v uint64) {
	switch c.immN {
	case 0:
		c.imm0 = v
	case 1:
		c.imm1 = v
	}
	c.immN++
}

// stageLaneIdx records a lane index, packing it above a memarg's memory index when the two
// staging words are already spoken for.
//
// **This exists because eight rows carry three values and the first draft dropped one**
// (grave #100).
// `v128.load8_lane` and its seven siblings are `memop` followed by `laneidx`
// (optable.go:433-440), and `memop` stages two words of its own — offset then memory index
// — so the lane index arrived as a third and `stage`'s switch discarded it. A shuffle
// operating on the wrong lane is a *different instruction than the module contains*, on
// valid input, which is the accept-direction class no board can see: every affected vector
// is one the suite expects to pass. Found by printing each row's staged-word demand rather
// than by trusting the sentence "no arm stages more than two", which was written as a
// claim and was false.
//
// The three values fit 128 bits and that is the whole argument for packing rather than
// growing Instr: an offset is a u64 (memopOffset), a memory index is a u32, and a laneidx
// is a `u8` (decode.ml:152) — 104 bits. So the offset keeps Imm0 and Imm1 carries the
// memory index in its low 32 bits with the lane above them, disjoint by width rather than
// by convention. Same move as immLane16 packing sixteen lanes into two words: packing is
// what makes 0002's two-word form sufficient for the whole table instead of only for its
// narrow arms.
func (c *instrCtx) stageLaneIdx(v uint64) {
	if c.immN < 2 {
		c.stage(v)
		return
	}
	c.imm1 |= (v & 0xFF) << memargLaneShift
}

// decline records a gated construct without returning it. First one wins, matching
// nonConst: it is the one a left-to-right reader would report.
func (c *instrCtx) decline(err error) {
	if c.declined == nil {
		c.declined = err
	}
}

// release returns the deferred feature decline, now that the grammar has completed.
//
// Its own method with a name, rather than an `if c.declined != nil` at each of the two
// call sites, because the *timing* is the whole content of this mechanism: called too
// early it reports a gate decline for a malformed module. A named release point is
// somewhere the rule can be stated once and somewhere a third caller has to think about.
func (c *instrCtx) release() error { return c.declined }

// constLegal reports whether one opcode may appear in a constant expression, and it is where
// extended-const's gate is read (#109).
//
// **The gate is checked here rather than through `gateCheck` because this is the only place that
// knows the position.** `gateCheck` sees an opcode and nothing else, so it cannot distinguish
// `i32.add` in a function body (MVP, ungated) from `i32.add` in a global's initializer (this
// proposal). Routing extended-const through the opcode map would decline the first, rejecting
// valid modules — which is exactly the accept-direction failure #109 was filed for, reintroduced
// by the fix. See gatemap.go's `gatedNonOpcodes` entry.
//
// **Gate off is not silence.** An extended-const opcode with the gate off is *declined by name*,
// via `decline`, and does not fall through to `nonConst` — because `constant expression required`
// is a spec **invalid** string and the module is well-formed and valid. Reporting it would lie
// about the module to conceal the engine's configuration, which is the #5 ruling. So the byte is
// const-legal for reporting purposes either way, and which error it produces is the gate's answer:
// on, it is accepted; off, `extended-const: feature gate disabled`.
//
// The decline is deferred like every other, so a malformed verdict further along still wins —
// binary.wast:112's reason, applied to a gate.
//
// # Grave: nine valid modules rejected, with the rule stated above the bug (#109)
//
// This function's predecessor returned `constOps[b]` alone, and `constOps`' comment asserted that
// extended-const "arrives with its gate" while no gate existed anywhere in `Features`. So
// `(data (i32.add (i32.const 0) (i32.const 42)))` was rejected with `constant expression required:
// 0x6a` — nine modules across `data.wast`, `elem.wast`, and `global.wast`, every one of them valid.
//
// **Neither board could see it, and that is a property of the corpus rather than an oversight.**
// All 4162 green vectors are `assert_malformed`, so a decoder that wrongly *rejects* scores full
// marks (contract §9 G-3); the all-on lane scores identically, because it also only asks rejection
// questions. What found it was `internal/gen/xcorpus`' accept-direction walk — 1954 independently
// produced images of must-succeed modules — which is why that control is product work rather than
// overhead.
//
// The lesson is *the defect stated as the rule*: review verifies code against its claims, and here
// the claim **was** the bug, so every reader who checked the two against each other found agreement.
// Print what the code returns; do not read what it says it returns.
func (c *instrCtx) constLegal(b byte) bool {
	if constOps[b] {
		return true
	}
	if !extendedConstOps[b] {
		return false
	}
	if !c.d.Features.ExtendedConst {
		c.decline(featureErr("extended-const"))
	}
	return true
}

// gateCheck records a decline if the opcode is gated and its gate is off.
//
// Called from both dispatch paths — single-byte and prefixed — because both read opcodes
// and the mapping covers both. A check on one path only would leave 0xfb and 0xfd, which
// is 306 of the 337 gated arms, accepted with their gates off.
func (c *instrCtx) gateCheck(prefix byte, sub uint32) {
	g, ok := gateFor(prefix, sub)
	if !ok {
		return
	}
	on, known := c.d.Features.enabled(g.gate)
	if !known {
		// A mapping entry naming a gate the Features switch does not handle. Loud
		// rather than treated as off: an unknown gate silently declining everything
		// would look like a working gate, and TestEveryFeatureFieldIsReadableByName
		// exists so this cannot ship — reaching it means the two halves disagree at
		// run time.
		c.decline(fmt.Errorf("%w: unmapped gate %q", errNoImmReader, g.gate))
		return
	}
	if !on {
		c.decline(featureErr(g.what))
	}
}

// decodeConstExpr reads a constant expression up to and including its END.
//
// The extent is discovered by reading instructions, not by trusting a length. Getting
// an immediate width wrong does not fail loudly — it shifts every byte after it, so
// the failure surfaces elsewhere as a size mismatch or a bogus opcode, which is why
// the element and data sections (which put an expression *before* other fields) made
// this worth an authority-derived table rather than a careful reading (#33 property 2).
func (d *Decoder) decodeConstExpr(r *reader) error {
	_, err := d.constExpr(r, false)
	return err
}

// decodeConstExprKeep reads a constant expression and returns it in internal form.
//
// The retaining twin of decodeConstExpr, and a separate entry point rather than a bool
// parameter on one, because the two have different *callers* rather than different
// behaviour. Sharing the body means the grammar has one definition site — the property
// grave #83 keeps being about.
//
// The split has held while the callers moved to this side one at a time: a global's initializer
// first, then a data segment's offset (0015), then an element segment's offset *and* its
// expression-form elements (0016). The non-retaining twin now has exactly one caller left, and
// when that goes the bool this refused to take will not need to exist either.
func (d *Decoder) decodeConstExprKeep(r *reader) ([]Instr, error) {
	return d.constExpr(r, true)
}

func (d *Decoder) constExpr(r *reader, keep bool) ([]Instr, error) {
	c := &instrCtx{d: d, constOnly: true, nonConst: -1}
	var out []Instr
	if keep {
		c.out = &out
	}
	err := c.constExprBody(r)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *instrCtx) constExprBody(r *reader) error {
	if err := c.block(r); err != nil {
		return err
	}
	if err := c.endTerminator(r); err != nil {
		return err
	}
	// The deferred verdicts, released only now that the grammar has agreed the bytes are
	// a well-formed expression, and in 0008's order: the feature decline outranks the
	// const verdict, because the engine's configuration is a more fundamental "no" than
	// a validation rule about a construct it does not implement.
	if err := c.release(); err != nil {
		return err
	}
	// `constant expression required` is an *invalid* string and the suite asserts it 24
	// times, always as assert_invalid — so this is a declared layering debt that moves
	// to #9's validator, not a malformed claim.
	if c.nonConst >= 0 {
		return fmt.Errorf("%w: %#02x", ErrConstExprRequired, c.nonConst)
	}
	return nil
}

// block reads instructions until a delimiter or the end of the image, mirroring
// `instr_block' s es` (decode.ml:967).
//
// `peek` does not consume, and it stops on ELSE as well as END — so a bare ELSE never
// reaches the dispatch below, and neither does a bare END. That is the whole reason
// the table's two `reason` arms are unreachable from here.
func (c *instrCtx) block(r *reader) error {
	for {
		b, ok := r.peek()
		if !ok || b == opElse || b == opEnd {
			return nil
		}
		if err := c.instr(r); err != nil {
			return err
		}
	}
}

// instr reads one instruction: the opcode, then its immediates.
func (c *instrCtx) instr(r *reader) error {
	b, err := r.byte()
	if err != nil {
		return err
	}
	info, ok := opTable[uint32(b)]
	switch {
	case !ok:
		// The catch-all, `| b -> illegal s pos b` (decode.ml:964). Absent from the
		// table is a verdict now, where it used to be indistinguishable from
		// present-but-not-const.
		return illegalOpcode(b)
	case info.illegal:
		// An arm the authority defines only in order to reject.
		return illegalOpcode(b)
	case info.reason != "":
		// Unreachable at bdd7164 and declared so rather than left silent — both such
		// arms (0x05, 0x0b) are the bytes `block` stops on. TestEveryReasonRowIsABlockDelimiter
		// is the tripwire for a third one arriving upstream.
		return fmt.Errorf("%w: %s", ErrMisplacedOpcode, info.reason)
	case info.escape:
		return c.prefixed(r, b)
	}
	// After the malformed verdicts and before the immediates: a gated opcode's
	// immediates are still read, because the grammar has to finish for a malformed
	// verdict further along to win. See instrCtx.declined.
	c.gateCheck(0x00, uint32(b))
	// Deferred, not returned: see decodeConstExpr. The *first* one is kept, because
	// that is the one a validator reading left to right would report.
	if c.constOnly && !c.constLegal(b) && c.nonConst < 0 {
		c.nonConst = int(b)
	}
	if err := c.imms(r, b, info.imms); err != nil {
		return err
	}
	// The structural arms emit themselves, because their extent is not known until the
	// nested block and its terminator have been read — and because the emitted form has
	// to place its END. See structural.
	if !info.isStructural() {
		c.emit(0x00, uint32(b))
	}
	return nil
}

// prefixed reads a sub-opcode after a prefix escape and dispatches into that region's
// sub-table.
//
// The sub-opcode is a **u32 LEB**, not a byte, and binary-leb128.wast:984 is the vector
// that says so: `\fc\87\80\80\80\80\00` is a six-byte encoding and the suite wants
// `integer representation too long`, which only a LEB read reports.
func (c *instrCtx) prefixed(r *reader, prefix byte) error {
	sub, err := r.u32()
	if err != nil {
		return err
	}
	region, ok := prefixRegion(prefix)
	if !ok {
		// The table says this byte escapes and no sub-table exists for it. A dispatch
		// the decoder cannot follow, which TestPrefixRegionsCoverTheTable makes a
		// build-time failure — so reaching it means the two halves disagree at run
		// time, and reporting the prefix is the least misleading thing available.
		return illegalOpcode(prefix)
	}
	info, ok := region[sub]
	switch {
	case !ok, info.illegal:
		return illegalPrefixed(prefix, sub)
	case info.escape:
		// No nested escape exists at bdd7164; a prefix inside a prefix region would
		// need its own sub-table, which prefixRegion does not model.
		return illegalPrefixed(prefix, sub)
	}
	// The region gates — 306 of the 337 mapped arms are here, so a gate check on the
	// single-byte path alone would have covered 31 of them.
	c.gateCheck(prefix, sub)
	// No prefixed row is structural — the four recursing arms are all single-byte — and
	// that is asserted rather than assumed: TestStructuralArmsAreExactlyTheBlockRows walks
	// every region, so a structural row appearing under a prefix upstream fails the build
	// rather than reaching `structural` with a fabricated opcode.
	if info.isStructural() {
		return fmt.Errorf("%w: structural row under prefix %#02x", errNoImmReader, prefix)
	}
	// `flags` is zero for every row but the br_on_cast pair, and it is read by castTypes
	// below rather than recovered from a staged word: the staging slots are Instr's wire
	// form and reading one back here would make this file reason about a slot assignment
	// that 0027 decision 1 says is *printed, never reasoned about*.
	var flags byte
	switch {
	case prefix == 0xFB && (sub == brOnCast || sub == brOnCastFail):
		f, err := c.brOnCastImms(r, info.imms)
		if err != nil {
			return err
		}
		flags = f
	default:
		if err := c.imms(r, 0x00, info.imms); err != nil {
			return err
		}
	}
	// #22, closed here rather than guessed at from a byte scan. Four opcodes reference
	// the data index space — memory.init (fc 08), data.drop (fc 09), array.new_data
	// (fb 09), array.init_data (fb 12), per interpreter/syntax/free.ml:165,166,175,181
	// — and a module using any of them without a data count section is malformed
	// (decode.ml:1299). The old deferral said a byte scan for `fc 08` would
	// false-positive on any immediate holding those bytes; decoding the body is what
	// makes the question answerable instead of approximable.
	if dataRefOps[[2]uint32{uint32(prefix), sub}] {
		c.d.sawDataRef = true
	}
	c.castTypes(prefix, sub, flags)
	c.emit(prefix, sub)
	return nil
}

// brOnCast and brOnCastFail are named because they are dispatched on by opcode identity in
// three places, and a bare 0x18 reads as a lane index or a section id at a glance.
//
// **Dispatched on the opcode and not on the immediate shape, deliberately.** `immByte`
// appears in exactly two rows of the whole table — these two — so keying off it would work
// today and be wrong the moment a row carries a raw byte that is not a flags word: the mask
// check below would then *reject a legal module*, which is the accept-direction failure §9
// G-3 calls worse than missing an invalid one. The reference dispatches the same way
// (`0x18l | 0x19l as opcode ->`, decode.ml:636), and *scope controls to the space* says the
// predicate must be the one the authority uses rather than the one this table's current
// contents make sufficient.
const (
	brOnCast     uint32 = 0x18
	brOnCastFail uint32 = 0x19
)

// brOnCastImmSeq is what the generated table must hold for both br_on_cast rows, and what
// brOnCastImms hand-codes. Two authorities, one fact, checked against each other rather
// than trusted — the shape `structural` already has, for the same reason: a hand-written
// reader beside a generated table is two places knowing one grammar.
var brOnCastImmSeq = []imm{immByte, immIdx, immHeapType, immHeapType}

// brOnCastImms reads `br_on_cast`/`br_on_cast_fail`'s immediates and returns the flags byte.
//
// # Why this pair cannot go through imms
//
// `imms` reads a flat sequence, and this row is not flat in two independent ways
// (decode.ml:640-650):
//
//	let flags = byte s in
//	require (flags land 0xfc = 0) s (pos + 2) "malformed br_on_cast flags";
//	let x = at var s in
//	let rt1 = ((if bit 0 flags then Null else NoNull), heaptype s) in
//	let rt2 = ((if bit 1 flags then Null else NoNull), heaptype s) in
//
// First, the requirement sits **between** the byte and the label — a module with reserved
// bits set is malformed at `pos + 2` whatever follows, so a reader that validated after the
// sequence would report the label's or a heaptype's error on an input the reference rejects
// before reaching either. Second, the byte's two low bits are the nullability of the two
// heaptypes that come *after* it, so the sequence's fourth element depends on its first.
// Neither fact has anywhere to live in a `[]imm` walk, which is the deferral `castTypes`'
// comment recorded at the previous revision; this is its redemption.
//
// The table's row is still the authority for *what* the immediates are — asserted, not
// assumed, so a grammar change upstream fails the build here instead of being read flat
// against a hand-written sequence that no longer matches.
func (c *instrCtx) brOnCastImms(r *reader, ims []imm) (byte, error) {
	if !slices.Equal(ims, brOnCastImmSeq) {
		return 0, fmt.Errorf("%w: br_on_cast row is %v, want %v", errNoImmReader, ims, brOnCastImmSeq)
	}
	flags, err := r.byte()
	if err != nil {
		return 0, err
	}
	// The mask, not `flags != 0`: bits 0 and 1 are grammatical content (the two null bits),
	// and `br_on_cast $l anyref (ref i31)` encodes 0x01 — see ErrMalformedBrOnCastFlags.
	if flags&0xfc != 0 {
		return 0, fmt.Errorf("%w: %#02x", ErrMalformedBrOnCastFlags, flags)
	}
	c.stage(uint64(flags))
	if err := c.imm(r, immIdx); err != nil {
		return 0, err
	}
	// rt1 then rt2, in the reference's order, both appended to c.heaps by the immediate arm.
	// The nullability is applied by castTypes, which is the one place a cast type is built.
	for range 2 {
		if err := c.imm(r, immHeapType); err != nil {
			return 0, err
		}
	}
	return flags, nil
}

// castTypes turns the heaptypes the cast family's grammar just read into the reference types it
// actually tests against, and stages them for `emit` to file.
//
// # Why the nullability is applied here and not in the immediate arm
//
// Nothing in this family encodes a reftype, and the null bit therefore comes from somewhere else
// in every row — but **not from the same somewhere**, which is the distinction the previous
// revision of this paragraph flattened. `decode.ml:636-639` reads a bare `heaptype` for each of
// `fb 14`-`fb 17` and pairs it with `NoNull` or `Null` chosen by *which of the four opcodes it
// is*; `:642-650` reads a flags **byte** and takes rt1's and rt2's bits out of the encoding.
// So for four rows the bit is a fact about the opcode and for two it is a fact about the input,
// and a sentence saying "either way, the opcode" is the defect-stated-as-the-rule shape — right
// about the four rows that existed when it was written, wrong as the family-wide claim it was
// phrased as. Its sibling in module.go's `Casts` comment said the same thing and is corrected in
// the same diff.
//
// What is uniform, and what actually justifies the site, is that neither fact reaches `imms`:
// that walk dispatches on immediate *kind* and holds no opcode and no earlier byte's value.
// Deriving the bit at each consumer instead would put a wire-format rule in the interpreter and
// in the encoder both, which is the two-places-know-one-fact shape this package files tripwires
// against.
//
// # Called for every prefixed opcode and answering for six
//
// The membership test is a switch rather than a table lookup, because the mapping *is* the
// nullability and a table would hold the same three columns in a shape that reads as data. Rows
// this switch does not name stage nothing and file nothing — including `ref.null` (0xD0), which
// is not prefixed and so never arrives here at all, and which keeps no heaptype by 0027 decision
// 4 in any case.
//
// `fb 18`/`fb 19` are here now, and they are the reason this function takes a flags byte. The
// previous revision deferred them with the note that their malformedness requirement sits
// *before* the label and so cannot be expressed by a flat `imms` walk — see brOnCastImms, which
// redeems that deferral. Every other row passes `flags` as zero and never reads it.
//
// # The pair files two types, and their order is load-bearing
//
// `rt1` is the branch instruction's *input* type and `rt2` is what it casts to (decode.ml:644-645),
// so a consumer reading element 0 where it wants element 1 gets a type that is legal, plausible,
// and wrong — `br_on_cast $l anyref (ref i31)` would test against `anyref`, which every value
// satisfies. The arms therefore do not receive this slice: `branchCastTargetAt` hands them `rt2`
// alone (interp/castop.go), so the wrong read has no syntax rather than a comment telling it not
// to. The full pair stays available here because #9's validator genuinely needs `rt1`.
func (c *instrCtx) castTypes(prefix byte, sub uint32, flags byte) {
	if prefix != 0xFB {
		return
	}
	// The length guards are unreachable on this path — `prefixed` returns before calling this
	// on any input where a heaptype arm failed — and they are kept for the same reason the
	// previous revision kept one: a filing site that indexes a staging slice states the extent
	// it requires, so a future caller that files before reading gets nothing rather than a
	// neighbour's heaptype.
	switch sub {
	case 0x14, 0x16: // ref.test, ref.cast — `NoNull` (decode.ml:636,638)
		if len(c.heaps) < 1 {
			return
		}
		c.casts = append(c.casts, refNull(c.heaps[0], false))
	case 0x15, 0x17: // ref.test null, ref.cast null — `Null` (decode.ml:637,639)
		if len(c.heaps) < 1 {
			return
		}
		c.casts = append(c.casts, refNull(c.heaps[0], true))
	case brOnCast, brOnCastFail:
		// **Nullability from the flags byte's bits, not from the opcode.** `bit 0 flags` is
		// rt1's and `bit 1 flags` is rt2's (decode.ml:644-645), which is why 0x18 and 0x19
		// share an arm here where 0x14-0x17 split into two: the four single-cast rows encode
		// their null bit in the opcode, the pair encodes both in the byte, and the same
		// instruction can be nullable on one side and not the other.
		if len(c.heaps) < 2 {
			return
		}
		c.casts = append(c.casts,
			refNull(c.heaps[0], flags&0x01 != 0),
			refNull(c.heaps[1], flags&0x02 != 0))
	default:
		return
	}
	c.hasCasts = true
}

// dataRefOps is the set of opcodes whose free variables include the data index space,
// which is what `data count section required` is actually about.
//
// Derived from the authority's own answer rather than from the two opcodes the suite
// happens to exercise: `free.ml` is where the reference computes it, and it names four.
// binary.wast:302 and :325 cover the `fc` pair; the `fb` pair has no vector, which is
// exactly the accept-direction blind spot 0007 exists for — a set written from the
// vectors would silently accept a GC module that the reference rejects.
var dataRefOps = map[[2]uint32]bool{
	{0xfc, 0x08}: true, // memory.init — free.ml:165
	{0xfc, 0x09}: true, // data.drop   — free.ml:166
	{0xfb, 0x09}: true, // array.new_data  — free.ml:175
	{0xfb, 0x12}: true, // array.init_data — free.ml:181
}

// isStructural reports whether a row is one of the four arms that recurse through the
// instruction grammar — block, loop, if, try_table.
//
// A method here rather than a field in optable.go, because that file is generated and
// this is not a fact the extractor reads from the authority: it is `imms`'s own dispatch
// predicate, given a name. Keyed off `immBlock` exactly as `imms` keys off it, so the two
// cannot disagree about which arms recurse — *one concept, one trigger* (#82). The set is
// pinned against the table by TestStructuralArmsAreExactlyTheBlockRows.
func (o opInfo) isStructural() bool {
	for _, im := range o.imms {
		if im == immBlock {
			return true
		}
	}
	return false
}

// imms reads an immediate sequence in the table's order.
//
// Order is the fact the table exists to carry: a wrong order shifts every subsequent
// byte, and that surfaces somewhere else entirely rather than here.
func (c *instrCtx) imms(r *reader, op byte, ims []imm) error {
	// The structural arms, keyed off immBlock's presence rather than off an opcode
	// list — the marker is the shape, so a fifth such arm upstream is caught by
	// TestStructuralArmsAreExactlyTheBlockRows instead of being read flat.
	if (opInfo{imms: ims}).isStructural() {
		return c.structural(r, op, ims)
	}
	for _, im := range ims {
		if err := c.imm(r, im); err != nil {
			return err
		}
	}
	return nil
}

// structural reads the four arms that recurse: block, loop, if, try_table.
//
// They cannot be table-driven because each ends with an `end_` that is not an
// immediate, and `if` reads its second block only when an ELSE is peeked
// (decode.ml:361-382, 412-417). The immediates *before* the first block are still the
// table's, so the hand-written part is only the recursion and the terminator.
func (c *instrCtx) structural(r *reader, op byte, ims []imm) error {
	blocks := 0
	for _, im := range ims {
		if im == immBlock {
			blocks++
			continue
		}
		if err := c.imm(r, im); err != nil {
			return err
		}
	}
	// **Emitted before the nested block, not after**, and this is the one ordering
	// decision in the retention that is not forced by the grammar. 0002's form resolves
	// branch targets to indices in the instruction slice, so a `block` has to occupy the
	// slot *preceding* its body — emitting after the recursion would put the header after
	// everything it encloses and make every branch target wrong by the body's length.
	//
	// Its own terminator is emitted by the `endTerminator` call below — see there for why
	// the delimiters are retained rather than dropped once they have been judged.
	//
	// **Grave #99, and the sentence that used to be here is the grave.** It read "its own
	// terminator is emitted by the recursive `block`/`expectEnd` pair below, which is why
	// END appears in the retained sequence at all" — and nothing emitted it. The defect
	// stated as the rule: a reviewer checking the code against that claim finds a `block`
	// call and an `expectEnd` call sitting exactly where the sentence says they are, so
	// review confirms it. What found it was an assertion over the accept population.
	c.emit(0x00, uint32(op))
	if err := c.block(r); err != nil {
		return err
	}
	// Two blocks means `if`: the else arm is present only when ELSE is actually next.
	// The reference's `expect 0x05 s "ELSE or END opcode expected"` here cannot fail —
	// it re-reads the byte it just peeked — so that message has no sentinel, which is
	// the honest reading of a dead branch in the authority.
	if blocks > 1 {
		if b, ok := r.peek(); ok && b == opElse {
			if _, err := r.byte(); err != nil {
				return err
			}
			// Retained for END's reason, one arm over: without it the then-arm and the
			// else-arm are one undifferentiated run of instructions, and an `if` whose
			// arms cannot be told apart executes the wrong one. A consumer could not
			// recover the split by counting, either — the arms have no declared lengths.
			c.emit(0x00, opElse)
			if err := c.block(r); err != nil {
				return err
			}
		}
	}
	return c.endTerminator(r)
}

// imm reads one immediate.
//
// The switch is exhaustive over the vocabulary by construction: the default arm is a
// hard error rather than a skip, so a new immediate arriving in the generated table
// fails here instead of being silently consumed as nothing. That is the same inversion
// the extractor's unrecognized-arm error makes — an omission must be loud.
func (c *instrCtx) imm(r *reader, im imm) error {
	switch im {
	case immIdx, immU32:
		v, err := r.u32()
		if err != nil {
			return err
		}
		c.stage(uint64(v))
		return nil
	case immS32:
		v, err := r.s32()
		if err != nil {
			return err
		}
		// Sign-extended into the slot, not zero-extended: an i32.const is a *signed*
		// LEB, and the interpreter's i32 slots are the low 32 bits of a uint64 with the
		// sign carried above them. Truncating to uint32 here would make -1 read as
		// 0xFFFFFFFF at 64 bits, which is the same value at 32 and a different one the
		// moment anything widens it.
		c.stage(uint64(int64(v)))
		return nil
	case immS64:
		v, err := r.s64()
		if err != nil {
			return err
		}
		c.stage(uint64(v))
		return nil
	case immF32:
		b, err := r.bytes(4)
		if err != nil {
			return err
		}
		// The bit pattern, verbatim and little-endian, never a float32 conversion: a
		// signalling NaN's payload survives a bit copy and does not survive a round trip
		// through a Go float, and the suite has vectors that assert exact NaN payloads.
		c.stage(uint64(binary.LittleEndian.Uint32(b)))
		return nil
	case immF64:
		b, err := r.bytes(8)
		if err != nil {
			return err
		}
		c.stage(binary.LittleEndian.Uint64(b))
		return nil
	case immV128:
		b, err := r.bytes(16)
		if err != nil {
			return err
		}
		// Both halves, low first. A v128 is the one immediate that fills Instr's two
		// words exactly, which is what makes the two-word shape sufficient rather than
		// merely convenient.
		c.stage(binary.LittleEndian.Uint64(b[:8]))
		c.stage(binary.LittleEndian.Uint64(b[8:]))
		return nil
	// DECLARED AND TRACKED (#262): unreachable from this walk as of rung 5 slice 2, and
	// retained. `immByte` is declared by exactly two rows — `0x18`/`0x19` — and both are
	// dispatched by opcode identity before `imms` runs, because the flags byte's low bits
	// govern the two heaptypes after it (see brOnCastImms). So this arm has declaring rows
	// and no path. Not deleted: `immVocabulary`'s comment records the `immValType`
	// precedent for why a live arm with no row stays (a row-derived domain drops the
	// entry, and the immediate then sums as zero bits the day a row arrives), and the
	// default arm being a hard error means deleting this case would turn a future
	// raw-byte row into a decode failure on a legal module — the accept direction.
	case immByte:
		v, err := r.byte()
		if err != nil {
			return err
		}
		c.stage(uint64(v))
		return nil
	case immLaneIdx:
		// `laneidx s = u8 s = uN 8` (decode.ml:152,103) — a LEB whose canonical form is
		// one byte and whose legal form runs to two. Grave #47 read it as a raw byte.
		v, err := r.uleb(8)
		if err != nil {
			return err
		}
		// Through stageLaneIdx, because the eight `v128.loadN_lane` rows reach here with
		// both words already staged by their memarg. See stageLaneIdx.
		c.stageLaneIdx(v)
		return nil
	case immLane16:
		// `repeat 16 laneidx s` (decode.ml:699): sixteen laneidx reads, so 16..32 bytes.
		//
		// **Packed into the two words rather than staged sixteen times**, because each
		// laneidx is a `u8` and sixteen of them are exactly 128 bits. Staging them
		// individually would silently drop fourteen — `stage` keeps the first two — and a
		// shuffle mask missing fourteen lanes is a *different instruction* than the module
		// contains. Packing is what makes the two-word Instr sufficient for i8x16.shuffle
		// rather than merely sufficient for the arms that happen to be narrow.
		//
		// Low lane in the low byte of Imm0, matching immV128's little-endian layout, so a
		// shuffle mask and a v128 constant are the same sixteen bytes read the same way.
		var lanes [2]uint64
		for i := range 16 {
			v, err := r.uleb(8)
			if err != nil {
				return err
			}
			lanes[i/8] |= (v & 0xFF) << (8 * (i % 8))
		}
		c.stage(lanes[0])
		c.stage(lanes[1])
		return nil
	case immValType:
		if err := c.d.decodeValType(r); err != nil {
			return err
		}
		// Packed into one word: kind in the low 8 bits, null in bit 8, and (for the
		// indexed form only) idx in bits 32-63 — the same three-field layout BlockType's
		// comment describes, chosen so this arm (unused by any row at the pinned
		// revision per immStagedBits' note, but a live reader per instrCtx.imm) does not
		// need a second word for a struct that still fits comfortably in one.
		word := uint64(c.d.valType.kind)
		if c.d.valType.null {
			word |= 1 << 8
		}
		word |= uint64(c.d.valType.idx) << 32
		c.stage(word)
		return nil
	case immHeapType:
		// `let ht = heaptype s` (decode.ml:603, :636-639) — `heaptype`, not `reftype`.
		//
		// This read `decodeRefType`, and the two productions disagree in **both**
		// directions: `heaptype`'s first branch is a type index, which `reftype` has no
		// arm for, so `ref.null 0` was rejected `malformed reference type: 0x00`; and
		// `reftype` has the `-0x1c`/`-0x1d` parameterized prefixes, which `heaptype` has
		// no arm for, so `ref.null (ref null extern)` was *accepted* — the same wrong
		// reader over-accepting at one end while under-accepting at the other. The
		// over-accept half is not in #88's original diagnosis; it turned up when the
		// probe was pointed at the fix rather than at the defect (#88).
		//
		// The comment this replaces declared the reader partial and deferred widening to
		// "the GC gate's business" — declared-and-tracked, so not a grave (#6), except
		// that the tracking pointed at gate work while the fix was substituting a reader
		// this file already had. *A deferral outlives its reason silently.*
		//
		// **Staged into `heaps`, and still claiming no word.** The decoded heaptype is retained
		// for the cast family (0027 Q1 option B) through a side table, not through Imm0/Imm1, so
		// `immStagedBits` keeps costing this kind zero — which is what lets `br_on_cast` read two
		// heaptypes at the 128-bit ceiling it already sits on. See instrCtx.heaps for why the
		// value staged here is a bare heaptype rather than the reftype a consumer wants.
		if err := c.d.decodeHeapType(r); err != nil {
			return err
		}
		c.heaps = append(c.heaps, c.d.valType)
		return nil
	case immBlockType:
		// The blocktype as the reference reads it: a non-negative type index, the empty
		// form, or a valtype (see decodeBlockType). Staged raw rather than normalized —
		// mapping a valtype blocktype to a synthetic single-result functype is the
		// *validator's* arity computation, and doing it here would put #9's work in the
		// decoder under a name that hides it.
		//
		// **Two words staged, not one, since 0018's implementation.** The second is the
		// valtype form's resolved index (module.go's BlockType comment), zero and unread
		// for the other two forms; staging it unconditionally is what makes Imm1 free for
		// exactly this purpose across every structural opcode
		// (TestBlockTypeImm1IsFreeForStructuralOpcodes), rather than staged only on the
		// branch that needs it.
		bt, btIdx, err := c.d.decodeBlockTypeValue(r)
		if err != nil {
			return err
		}
		c.stage(bt)
		c.stage(btIdx)
		return nil
	case immVecValType:
		// select's optional result-type vector, retained in the side table `emit` files it into
		// (0016) since **#294** — the day this comment's own deferral named.
		//
		// The deferral read: "nothing consumes the full vector, `select` needs its arity from the
		// *stack* at validation time, and a vector does not fit two words (#7)", then corrected
		// itself to "what keeps the full vector discarded is the *absence* of a consumer, not the
		// absence of a mechanism … the retention it wants is its own field, on the day #9 needs the
		// annotation." Both halves are worth keeping, because the first one's middle clause was
		// **wrong on the spec and not merely premature**: `select`'s arity does not come from the
		// stack in the annotated form. `valid.ml:442-446` types the operands against `ts` and
		// requires `List.length ts = 1`, so a validator reading the stack would have accepted
		// `(select (result i32 i32) …)` — the accept-direction hazard (§9 G-3) that a stack-shape
		// guess always risks, recorded here as a *plan* while no validator existed to be wrong. Its
		// retraction is why the arm below stages the vector and not a summary of it.
		//
		// **One bit *is* staged, since #196/#197, and it is not the full annotation.** `select`
		// has no static type to consult at runtime (this package's own layer has no validator),
		// so the interpreter's own dispatch — numeric/vector operands on `st.num`, reference
		// operands on `st.refs` — cannot be made safely from stack shape alone: a live
		// reference sitting elsewhere on `st.refs` while an unrelated numeric `select` executes
		// would misdispatch, which is exactly the accept-direction hazard (§9 G-3) a stack-shape
		// guess risks. `valid.ml:442`'s own rule caps the vector at arity 1
		// ("invalid result arity other than 1 is not (yet) allowed"), so the one case this
		// engine's decoder needs to answer is "is that single type a reference" — Imm0 is 1 for
		// yes, 0 for every other shape (arity 0, or a numeric/vector type), which the interpreter
		// reads as `ins.Imm0 != 0` rather than re-deriving from a runtime guess.
		//
		// **The bit survives the retention as a cache, and the last element is deliberately
		// what it summarizes.** With the vector filed, `isRef` is derivable at every consumer;
		// what it buys is the interpreter's map-free dispatch, which is the whole reason it was
		// staged. The two facts are checked against each other by
		// `TestSelectImm0AgreesWithTheAnnotation` rather than trusted, because a cache and its
		// source in different words of different tables is exactly the pair that drifts. On the
		// only arity the validator admits the two readings coincide; for arity > 1 the bit reads
		// the *last* element, which is stated here rather than left for a reader to infer from
		// the assignment's position in the loop.
		c.hasSelects = true
		var isRef bool
		if err := c.d.decodeVec(r, func(r *reader) error {
			if err := c.d.decodeValType(r); err != nil {
				return err
			}
			isRef = c.d.valType.IsRef()
			// Appended per element, never preallocated from the declared count — grave #138's
			// law, for the reason `immVecIdx` gives one arm over: a vector claiming 0xFFFFFFFE
			// types stays bounded by the image only as long as nothing sizes a slice from the
			// count first.
			c.selects = append(c.selects, c.d.valType)
			return nil
		}); err != nil {
			return err
		}
		if isRef {
			c.stage(1)
		} else {
			c.stage(0)
		}
		return nil
	case immVecIdx:
		// br_table's label vector, retained in the side table `emit` files it into (0016).
		// It cannot live in Instr's two words, and it now has a consumer — the interpreter's
		// br_table arm and the text encoder's, #8.
		//
		// **Appended per element, never preallocated from the declared count**, and that is
		// grave #138's law applied before the defect rather than after it. `decodeVec` reads
		// a u32 count and then requires each element to consume bytes, so a huge count is
		// bounded by the *image*: a vector claiming 0xFFFFFFFE labels runs out of input long
		// before it allocates. A `make([]uint32, 0, n)` here would reintroduce exactly #138's
		// shape — a count check right about the verdict and wrong about the resources — at
		// four bytes per phantom label, which is 16 GiB from a five-byte immediate.
		c.hasLabels = true
		return c.d.decodeVec(r, func(r *reader) error {
			l, err := r.u32()
			if err != nil {
				return err
			}
			c.labels = append(c.labels, l)
			return nil
		})
	case immCatchVec:
		// try_table's handler clauses, retained in the side table `emit` files them into,
		// mirroring immVecIdx's br_table case exactly: a clause is a tag index plus a label
		// rather than a bare label, which is why it has its own side table (Catches) rather
		// than a share of Labels — #199's rung 1, closing the gap `immVecIdx`'s neighbour
		// comment used to name.
		//
		// **Appended per element, never preallocated from the declared count**, on
		// immVecIdx's own citation of grave #138: `decodeVec` reads a u32 count and then
		// requires each element to consume bytes, so a huge declared count is bounded by the
		// *image* rather than trusted for an allocation.
		c.hasCatches = true
		return c.d.decodeVec(r, func(r *reader) error {
			cl, err := decodeCatch(r)
			if err != nil {
				return err
			}
			c.catches = append(c.catches, cl)
			return nil
		})
	case immMemop:
		return c.decodeMemop(r)
	case immBlock:
		// Routed through structural, which is the only caller that can supply the
		// terminator. Reaching here would mean imms did not notice the marker.
		return fmt.Errorf("%w: block immediate outside a structural arm", errNoImmReader)
	}
	return fmt.Errorf("%w: %q", errNoImmReader, im)
}

// errNoImmReader is the switch's default arm, and it is deliberately **not** in
// declaredErrors.
//
// It is not a spec verdict and it is not a gate declining to judge — it is the engine
// saying it does not know how to read a field the table told it about, which is a bug in
// this file rather than a fact about the module. The first draft wrapped
// ErrMisplacedOpcode here, which was wrong in the way that costs later: a reader that
// does not exist has nothing to do with an opcode in the wrong place, and sharing the
// sentinel would have made TestEveryImmediateHasAProductionReader unable to tell "no
// arm" from "the arm ran and rejected these bytes" — a control that cannot distinguish
// its own failure mode from the code's.
//
// Keeping it out of declaredErrors is what makes reaching it a fuzz *find*. That is the
// inverse of the ErrDataCountRequired listing: declare an error that is unreachable but
// legitimate, and leave undeclared an error that would only ever be a defect.
var errNoImmReader = errors.New("internal: no reader for immediate")

// errNotEmptyBlockType is the blocktype alternation's middle branch declining, and it is
// deliberately not a spec sentinel: the reference's own text there is the **empty string**
// (`expect 0x40 s ""`, decode.ml:337), because that branch's failure is never reported —
// `either` is going to try the next one.
//
// It previously reused ErrMalformedValType, which was harmless while that string existed and
// became a lie when #88 established that the reference has no `valtype` message at all: the
// branch would have been reporting a *neighbouring production's* invented sentinel for a
// condition the reference declines to name. Undeclared in declaredErrors and unreachable by
// construction — the last branch always overwrites it — so its arrival at the surface is a
// fuzz find rather than a verdict, the same posture as errNoImmReader above (#88).
var errNotEmptyBlockType = errors.New("internal: blocktype is not the empty result type")

// decodeMemop reads a memarg: flags, an optional memory index, and an offset
// (decode.ml:324-332).
//
// The order of the two checks is the fact, and four binary-leb128.wast vectors turn on
// it: the reference reads the flags LEB *first* and only then requires the value below
// 0x80, so an overlong or over-wide flags encoding reports the LEB error and never
// reaches `malformed memop flags`. Writing the bound check as part of the read — a
// one-byte flags field, say — would score those four vectors with the wrong string.
//
// The offset is a **u64**. binary-leb128.wast:404 encodes it in eleven bytes and wants
// `integer representation too long`, which is one byte past a u64's budget and *two*
// past a u32's — so a u32 read gets the right verdict there and the wrong one at
// :730, where a ten-byte offset with unused bits set wants `integer too large`.
// Bit 6 is multi-memory's, and it is the one gated construct in this file that is not an
// opcode: `if bit 6 (the MSB of the first LEB byte) is set, then an i32 memory index
// follows` (proposals/multi-memory/Overview.md:65). So the decline is recorded on the ctx
// like every other, rather than returned here — a memarg is read mid-body and a malformed
// verdict further along still outranks it.
func (c *instrCtx) decodeMemop(r *reader) error {
	flags, err := r.u32()
	if err != nil {
		return err
	}
	if flags >= 0x80 {
		return fmt.Errorf("%w: %#02x", ErrMalformedMemopFlags, flags)
	}
	memIdx, err := c.memopIndex(r, flags)
	if err != nil {
		return err
	}
	off, err := memopOffset(r)
	if err != nil {
		return err
	}
	// **Offset in Imm0; memory index and alignment exponent in Imm1** — see module.go's
	// memarg packing comment for the word's three tenants and Memarg for reading them back.
	//
	// **The alignment is retained as of #306, and the argument it replaces was sound in its
	// premise.** Alignment really is a validation constraint with no execution semantics
	// (`valid.ml:380-389`), so this comment used to say that keeping it would store a fact
	// only #9 reads. That was a reason while #9 did not exist. Once it did, the same
	// sentence became a description of a defect: the validator knows how to reject
	// `align=4` on an `i32.load` and could not see the alignment to do it, so 54
	// `assert_invalid` vectors were accepted.
	//
	// Six bits, in a word with eighteen spare above them, is what the retention costs —
	// which is also why *this* is the fix rather than a side-table field: a map entry per
	// load and store is the allocation 0002's fixed-width form exists to avoid, and the
	// hottest instruction class in Wasm is the wrong place to start paying it.
	c.stage(off)
	c.stage(StageMemarg(memIdx, flags))
	return nil
}

// memopIndex reads the explicit memory index bit 6 selects, or returns 0 when the bit is
// clear — memory 0 implied.
//
// Split out so no `err` is live across the conditional read, which is the same shape
// decodeDataSegmentMode was split for and for the same pair of linters: reusing the outer
// `err` inside the branch trips gocritic's sloppyReassign and shadowing it trips govet's
// shadow, two enabled linters pointing opposite ways. Narrowing the scope is the fix both
// were asking for (decision 0005's spirit clause). Retention is what made this conditional
// read start producing a value at all, so the shape arrived with the retention.
//
// The precedent still holds and its subject has moved: decodeDataSegmentMode now *returns* a
// DataSegment (0015), so its own split is load-bearing for retention rather than only for the
// linters. Noted because a citation to a shape outlives the shape unless someone says so.
func (c *instrCtx) memopIndex(r *reader, flags uint32) (uint64, error) {
	if flags&0x40 == 0 {
		return 0, nil
	}
	if !c.d.Features.MultiMemory {
		c.decline(featureErr(gatedNonOpcodes[gateMultiMemory]))
	}
	idx, err := r.u32()
	if err != nil {
		return 0, err
	}
	return uint64(idx), nil
}

// memopOffset reads a memarg's offset, which is a **u64** — `memop` ends with
// `let offset = u64 s` (decode.ml:332).
//
// Its own function so the width has a definition site rather than only a call site. The
// difference is two vectors: at binary.wast:730 a ten-byte offset with unused bits set
// wants `integer too large`, which a u32 read reports as `too long`. Reading it as a u32
// refilled five vectors when probed.
func memopOffset(r *reader) (uint64, error) {
	return r.u64()
}

// decodeCatch reads one try_table handler clause and returns it in internal form
// (decode.ml:975-981):
//
//	| 0x00 -> let x = at idx s in let y = at idx s in Mnemonics.catch x y
//	| 0x01 -> let x = at idx s in let y = at idx s in catch_ref x y
//	| 0x02 -> let x = at idx s in catch_all x
//	| 0x03 -> let x = at idx s in catch_all_ref x
//
// Retention as of #199's rung 1 — the caller (immCatchVec's arm) used to call this for its
// grammar verdict alone and discard every index via `discardIndex`; every index is now kept
// in the returned Catch, on `Catch`'s own comment for why a Kind byte plus (up to) two
// indices is the whole shape the reference's AST carries for all four forms.
func decodeCatch(r *reader) (Catch, error) {
	kind, err := r.byte()
	if err != nil {
		return Catch{}, err
	}
	switch kind {
	case byte(CatchTag), byte(CatchTagRef): // catch, catch_ref: a tag and a label
		tag, err := r.u32()
		if err != nil {
			return Catch{}, err
		}
		label, err := r.u32()
		if err != nil {
			return Catch{}, err
		}
		return Catch{Kind: CatchKind(kind), TagIndex: tag, LabelIndex: label}, nil
	case byte(CatchAny), byte(CatchAnyRef): // catch_all, catch_all_ref: a label only
		label, err := r.u32()
		if err != nil {
			return Catch{}, err
		}
		return Catch{Kind: CatchKind(kind), LabelIndex: label}, nil
	default:
		return Catch{}, fmt.Errorf("%w: %#02x", ErrMalformedCatch, kind)
	}
}

// decodeBlockType reads a blocktype (decode.ml:334-339).
//
// `either` is a backtracking alternation — it resets the cursor and tries the next
// branch on *any* error — so this is written as one, rather than as a switch over the
// bytes each branch happens to accept. The difference shows on an overlong LEB: the
// s33 branch fails with a LEB error, the cursor rewinds, and the byte is judged again
// as 0x40-or-valtype, so the reported error comes from the *last* branch. A switch
// would report the first branch's error and be wrong about which rule rejected it.
//
// Branch order is the reference's. Because either backtracks, the order does *not* affect
// the accept set or any extent — measured over all 256 first bytes in both orders, 427 of
// 768 rows differ and every difference is the error message alone. What the order decides
// is which branch's error survives, and that is load-bearing in one place the suite cannot
// reach: **valtype must be last so a feature decline stands.** 0x7b with SIMD off is
// ErrFeatureDisabled from this branch; move it earlier and the alternation overwrites that
// with valtype's spec malformed-string — a gate manufacturing malformedness for a construct
// Wasm 3.0 defines. Pinned by TestBlockTypeAlternationIsTheAuthority, whose doc records the
// wrong reason this comment used to give.
//
// (Since #88, valtype is itself an `either` whose last branch is `reftype`, so the surviving
// string for a byte that is no blocktype at all is `malformed reference type`. The ordering
// fact is unchanged; the *name* of the message it selects moved one production deeper.)
func (d *Decoder) decodeBlockType(r *reader) error {
	_, _, err := d.decodeBlockTypeValue(r)
	return err
}

// decodeBlockTypeValue is decodeBlockType returning what it read, encoded across two words
// so a consumer can tell the three forms apart — and, for the valtype form, recover a
// GC-gated indexed result — without re-parsing.
//
// The encoding: a type index `i` is `(i, 0)`, the empty result type is `(blockTypeEmpty, 0)`,
// and a valtype `t` is `(blockTypeValType | uint64(kind) | null-bit, idx)` — see module.go's
// comment above the const block for the full packing rule and BlockType for the reader.
// Disjoint by construction, not by luck: a type index is `s33` and therefore below 2^32 when
// non-negative, so the two tag bits in the first word sit above every legal index, and the
// second word is meaningful only for the valtype form.
//
// The alternation writes it through Decoder fields for the reason decodeValType does:
// `either` takes `func(*reader) error` branches and cannot return a value from the one
// that matched.
func (d *Decoder) decodeBlockTypeValue(r *reader) (imm0, imm1 uint64, decodeErr error) {
	decodeErr = either(r,
		func(r *reader) error {
			// `typeuse s33` (decode.ml:160-164): a negative index is `malformed type
			// index`, which is what sends 0x40 and the valtypes to the next branch.
			v, err := r.sleb(33)
			if err != nil {
				return err
			}
			if v < 0 {
				return ErrMalformedTypeIndex
			}
			d.blockType, d.blockTypeIdx = uint64(v), 0
			return nil
		},
		func(r *reader) error {
			// `expect 0x40 s ""` — the empty result type. The reference's message is
			// literally empty, because this branch's failure is never reported: either
			// is going to try the next one.
			b, err := r.byte()
			if err != nil {
				return err
			}
			if b != 0x40 {
				return errNotEmptyBlockType
			}
			d.blockType, d.blockTypeIdx = blockTypeEmpty, 0
			return nil
		},
		func(r *reader) error {
			if err := d.decodeValType(r); err != nil {
				return err
			}
			kind, ok := d.valType.Kind()
			if !ok {
				// The indexed reference form: no single wire byte, so the tag carries
				// kindIndexed and the second word carries the resolved index — see
				// module.go's BlockType comment for why this needs Imm1 at all.
				kind = kindIndexed
			}
			word := blockTypeValType | uint64(kind)
			if d.valType.Null() {
				word |= blockTypeNullBit
			}
			d.blockType = word
			d.blockTypeIdx = uint64(d.valType.Index())
			return nil
		},
	)
	return d.blockType, d.blockTypeIdx, decodeErr
}

// either is `let rec either fs s` (decode.ml:126-131): try each branch, resetting the
// cursor after a failure, and let the last branch's error stand.
//
// The reset is the point. Without it a failed branch leaves the cursor mid-field and
// the next branch reads the wrong bytes — which does not fail loudly, it shifts
// everything after.
//
// **A feature decline is not a failure to match, so it is not backtracked.** The
// reference has no gates and therefore no equivalent of this branch, which is precisely
// why the divergence has to be reasoned rather than transcribed: `ErrFeatureDisabled`
// says "this engine declines a construct the grammar defines", where every other error
// here says "these bytes are not this production". Backtracking the first swaps a
// *configuration* answer for a *grammar* answer, and the grammar answer is the last
// branch's malformed-string — a gate manufacturing malformedness, which the #5 ruling
// forbids in exactly those words.
//
// Two sites, and the difference between them is why this lives here rather than being
// fixed by ordering at each one:
//
//   - `decodeBlockType` (instr.go) and `decodeHeapType` put the gated branch last, so
//     its decline already survived, and TestBlockTypeAlternationIsTheAuthority pins
//     that. Ordering was a sufficient remedy there because the reference's own order
//     happens to agree.
//   - `decodeStorageType` (#86) cannot use it. The reference puts `valtype` **first**
//     (decode.ml:236-241), and that order is load-bearing for a different reason — it
//     decides that a byte which is neither reports `malformed storage type` rather than
//     the valtype branch's message. So the two obligations pull opposite ways at one
//     site, and only propagation satisfies both.
//
// Measured over all 256 first bytes at both alternation sites in three lanes: the accept
// sets are **identical** before and after, and the only rows that change are ones this
// engine was answering with a spec malformed-string where its own configuration was the
// actual reason. A v128 array field with SIMD off reported `malformed storage type: 0x7b`
// and now reports `simd: feature gate disabled`.
//
// Found by probing the gate/either interaction while writing #86's print-checks, in code
// #86 itself had just added — the hazard the blocktype comment describes, at a site whose
// ordering could not be the answer. (#86.)
func either(r *reader, branches ...func(*reader) error) error {
	start := r.off
	var err error
	for _, f := range branches {
		r.off = start
		if err = f(r); err == nil {
			return nil
		}
		if errors.Is(err, ErrFeatureDisabled) {
			return err
		}
	}
	return err
}

// illegalOpcode renders `illegal opcode <hh>` — `illegal s pos b` with
// `string_of_byte = "%02x"` (decode.ml:35, 52).
//
// The rendering is load-bearing and, for once, oracle-covered: binary.wast:1218
// expects `illegal opcode ff`, so the byte is *inside* the expected string and the
// harness reads it. That is the one place the invented-bits class (grave #36) has
// suite teeth (#38); TestIllegalOpcodeRenderings prints the rest.
func illegalOpcode(b byte) error {
	return fmt.Errorf("%w %02x", ErrIllegalOpcode, b)
}

// illegalPrefixed renders an unknown or rejected sub-opcode, and the three regions do
// **not** agree on the shape.
//
// `illegal2 s pos b n` prints prefix *and* sub-opcode (decode.ml:53-54) and is what
// 0xfb and 0xfc fall through to (:655, :681). 0xfd falls through to plain `illegal s
// pos (I32.to_int_u n)` (:961), printing the sub-opcode **alone** — as do all nineteen
// of 0xfd's explicit illegal arms, which are the only explicit illegal arms in any
// sub-table. So `illegal opcode fb 20` and `illegal opcode 9a` are both correct, for
// different prefixes.
//
// That asymmetry is a fact about the authority's text, not a choice, and no vector in
// the phase-1 corpus reaches any of it — no `\fb` or `\fd` byte appears in the corpus
// at all. TestPrefixIllegalRenderingMatchesTheAuthority reads decode.ml and checks
// each region's fallthrough against this function, which is the only cover available
// where the oracle is silent.
func illegalPrefixed(prefix byte, sub uint32) error {
	if twoFieldIllegal[prefix] {
		return fmt.Errorf("%w %02x %02x", ErrIllegalOpcode, prefix, sub)
	}
	return fmt.Errorf("%w %02x", ErrIllegalOpcode, sub)
}

// twoFieldIllegal records which prefix regions reject with `illegal2` (prefix and
// sub-opcode) rather than plain `illegal` (sub-opcode alone).
//
// Hand-written because the extractor deliberately skips fallthrough arms — they bind a
// variable, not an opcode — so the fact is not in the generated table. It is therefore
// an *enrolled witness* rather than a third opinion: the citations below name the
// authority's lines, and TestPrefixIllegalRenderingMatchesTheAuthority derives the
// truth from decode.ml and fails if this map disagrees, scoped to every region in
// prefixRegions rather than to the two entries here.
var twoFieldIllegal = map[byte]bool{
	0xfb: true,  // `| n -> illegal2 s pos b n` — decode.ml:655
	0xfc: true,  // `| n -> illegal2 s pos b n` — decode.ml:681
	0xfd: false, // `| n -> illegal s pos (I32.to_int_u n)` — decode.ml:961
}

// expectEnd is `end_ s = expect 0x0b s "END opcode expected"` (decode.ml:322).
//
// Two vectors, two errors, from the same call: binary.wast:55 has a byte there that is
// not END and gets `END opcode expected`; binary.wast:76 has *no* byte there, and
// `expect` reads through `guard`, which converts the end of the image into `unexpected
// end of section or function` (decode.ml:44-45, 51). The reader's eof field is what
// carries that distinction here.
func expectEnd(r *reader) error {
	b, err := r.byte()
	if err != nil {
		return err
	}
	if b != opEnd {
		return fmt.Errorf("%w: %#02x", ErrEndExpected, b)
	}
	return nil
}

// endTerminator is expectEnd plus the retention: the verdict is the free function above,
// and this is the accepting path that keeps the byte.
//
// **Grave #99.** The first version of the retention had no such function: `expectEnd` read
// the terminator, judged it, and dropped it at all three call sites, so 23 of the 27 bound
// functions in the accept population decoded to a *zero-length body* — and `structural`'s
// comment claimed the opposite in so many words, which is why review did not find it. The
// lesson is at that comment's replacement; this is the mechanism.
//
// **The split is deliberate and the merged version was the bug.** `expectEnd` is a grammar
// check with two error vectors behind it and no business knowing about a retained
// sequence; `emit` is called only after a verdict has been reached. Wiring the retention
// into the free function would put an append in front of the error return — an instruction
// retained from a module the layer is about to reject.
//
// Why END is retained at all, since the reader treats it as a delimiter rather than an
// instruction: 0002's form resolves branch targets to indices in this slice, and a block's
// *extent* is not derivable from the header alone — the header records the blocktype, not
// the length. Dropping the terminator leaves the interpreter to recompute extents by
// re-walking the sequence, which is a second opinion about the program's structure and so
// the drift risk 0006 says to prefer away from. The same argument covers ELSE, whose
// absence would make an `if`'s two arms indistinguishable.
func (c *instrCtx) endTerminator(r *reader) error {
	if err := expectEnd(r); err != nil {
		return err
	}
	c.emit(0x00, opEnd)
	return nil
}

// decodeFuncBody reads one entry of the code section: a declared size, then locals,
// instructions, and END (decode.ml:1133-1140).
//
// `code_section s = section Custom.Code (vec (at (sized code))) [] s` — `sized` wraps
// **each body**, not the section, so a body whose grammar disagrees with its own
// declared length is `section size mismatch` even though no section boundary moved.
// binary.wast:92 is that vector and the suite explains it in a comment: the missing END
// makes the grammar consume the following data section's `\0b` as one, so the body
// reads four bytes longer than it declared.
//
// The grammar is bounded by the *image*, not by the declared body extent, which is what
// lets the over-read happen at all. See sections.go on why that is required rather than
// merely tolerated.
func (d *Decoder) decodeFuncBody(r *reader) error {
	// `sized` reads its length with len32, so an extent exceeding the image is
	// `length out of bounds` before any grammar runs — face 1 of the size mechanism,
	// one level down from a section.
	size, err := r.u32()
	if err != nil {
		return err
	}
	if uint64(size) > uint64(r.remaining()) {
		return fmt.Errorf("%w: %d bytes declared, %d left", ErrSectionOverrun, size, r.remaining())
	}
	start := r.off
	locals, err := d.decodeLocals(r)
	if err != nil {
		return err
	}
	var body []Instr
	var labels map[int][]uint32
	var catches Catches
	var casts map[int][]ValType
	var selects map[int][]ValType
	c := &instrCtx{
		d: d, nonConst: -1, out: &body,
		labelsOut: &labels, catchesOut: &catches, castsOut: &casts, selectsOut: &selects,
	}
	if err := c.block(r); err != nil {
		return err
	}
	if err := c.endTerminator(r); err != nil {
		return err
	}
	if used := r.off - start; used != int(size) {
		return fmt.Errorf("%w: function body declared %d bytes, grammar consumed %d",
			ErrSectionSizeMismatch, size, used)
	}
	// The deferred decline, released *after* the size reconciliation and not before.
	// Malformed outranks a feature decline, and the size mismatch is this layer's
	// malformed verdict — so a body that is both gated and mis-sized reports the size,
	// which is the answer that does not depend on the engine's configuration. Releasing
	// one line earlier would still be right for binary.wast:112 (the decline there is in
	// a *global* initialiser, a different call path) and wrong here, which is the shape
	// of ordering bug the suite would not catch: TestGateDeclineYieldsToMalformed is the
	// control, because no vector exercises it.
	if err := c.release(); err != nil {
		return err
	}
	// The body is retained only now, past every verdict this layer can return: appending
	// before the size reconciliation would keep a body from a module the decoder is about
	// to reject. The zip with the function section's type indices happens in finishFuncs,
	// because the other half is not available until both sections are read — see
	// Decoder.funcTypeIdx.
	d.mod().Funcs = append(d.mod().Funcs,
		Func{Locals: locals, Body: body, Labels: labels, Catches: catches, Casts: casts, Selects: selects})
	return nil
}

// decodeLocals reads a body's local declarations (decode.ml:341-351).
//
// Two different fields, two different errors, and the suite pins both:
//
//   - The per-group *count* is a u32 LEB, so binary.wast:125's `\80\80\80\80\10` is
//     `integer too large` — the count field's own width, judged by the LEB reader.
//   - The *sum* over all groups is required below 2^32 and computed at **64 bits**
//     (`I64.lt_u (fold_left I64.add 0L ns) 0x1_0000_0000L`), so binary.wast:159's
//     0xFFFFFFFF + 2 and :175's four groups of 0x40000000 are `too many locals`. A sum
//     accumulated at 32 bits would wrap and accept both.
//
// It returns the declared **groups**, one per `(count, valtype)` run — the wire form, not a
// flattened vector.
//
// # It used to flatten, and that was grave #138
//
// The old body allocated `make([]ValType, 0, total)` and appended `count` copies per group,
// after the sum check. The prose here argued the *order* was load-bearing — flatten only
// once `too many locals` has had its say, so that a body declaring 0xFFFFFFFF locals does
// not allocate four billion entries for a module the next line refuses, which
// `binary.wast:159` and `:175` are the vectors for.
//
// Every word of that was true and it defended the wrong side of the boundary. The check
// rejects `>= 1<<32`; `0xFFFFFFFE` is **admitted**, and at one byte per `ValType` that is a
// 30-byte image decoding successfully into 4.00 GiB. The sum check was doing verdict work
// and was silently credited with resource work it never did. *A count check that is right
// about the verdict can still be wrong about the resources* — and the comment reading as a
// proof is what kept it invisible through review.
//
// The sum check stays exactly as it was, because it was never the defect: it is the
// reference's bound, it is the suite's answer for :159 and :175, and those two vectors still
// pin it. What changed is that nothing downstream expands the count.
func (d *Decoder) decodeLocals(r *reader) ([]LocalGroup, error) {
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	var (
		groups []LocalGroup
		total  uint64
	)
	for range n {
		count, err := r.u32()
		if err != nil {
			return nil, err
		}
		if err := d.decodeValType(r); err != nil {
			return nil, err
		}
		total += uint64(count)
		groups = append(groups, LocalGroup{Count: count, Type: d.valType})
	}
	// Computed at 64 bits and compared against 2^32, per the two vectors above. Note this is
	// now the *only* place a local count is aggregated: with the flattening gone, the sum is
	// a verdict and nothing else, which is what it always should have been.
	if total >= 1<<32 {
		return nil, fmt.Errorf("%w: %d", ErrTooManyLocals, total)
	}
	return groups, nil
}
