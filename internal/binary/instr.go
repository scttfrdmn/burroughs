package binary

import (
	"encoding/binary"
	"errors"
	"fmt"
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
// So constWalk defers: it records the first non-const instruction and returns it only
// if the grammar completed. *An invalid verdict that pre-empts a malformed one is
// reporting the wrong layer's answer* — the same error as an error from the wrong
// layer (#36), pointed the other way.
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
// **The sentence that used to stand here said extended-const did too, and it does not** (#109).
// There is no extended-const field in `Features`, so `i32.add` in a constexpr is not declined
// by a gate — it is rejected outright, with `constant expression required`, on nine modules the
// suite requires accepted (`data.wast:178`, `elem.wast:1057`, `global.wast:3`, and six more).
// The claim read as *declared and tracked* and licensed the omission, which is the
// defect-stated-as-the-rule shape: review verifies code against claims, and this claim was
// false. Found by the #67 cross-check corpus, because every one of the 4162 green vectors is a
// rejection and no board can see a decoder that wrongly rejects (contract §9 G-3).
//
// Whether the answer is a ninth gate or a grammar exclusion is Scott's call in #109 — G-2 does
// not name extended-const, though it is Wasm 3.0 core. Either way the *string* is wrong: a
// declined feature reports a feature-named error, never a spec `invalid` string (#5).
var constOps = map[byte]bool{
	0x41: true, // i32.const
	0x42: true, // i64.const
	0x43: true, // f32.const
	0x44: true, // f64.const
	0x23: true, // global.get
	0xD0: true, // ref.null
	0xD2: true, // ref.func
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
}

// emit appends one decoded instruction to the retained sequence and clears the staging
// fields.
//
// Called on the *accepting* path only — after the opcode's malformed verdicts and after
// its immediates have been read without error. An instruction emitted before its
// immediates were read would be an instruction the module might not contain.
func (c *instrCtx) emit(prefix byte, op uint32) {
	if c.out != nil {
		*c.out = append(*c.out, Instr{Op: op, Prefix: prefix, Imm0: c.imm0, Imm1: c.imm1})
	}
	c.imm0, c.imm1, c.immN = 0, 0, 0
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
	c.imm1 |= (v & 0xFF) << 32
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
// behaviour: a global's initializer has a consumer, an element segment's offset does not
// yet (#7). Sharing the body means the grammar has one definition site — the property
// grave #83 keeps being about.
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
	if c.constOnly && !constOps[b] && c.nonConst < 0 {
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
	if err := c.imms(r, 0x00, info.imms); err != nil {
		return err
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
	c.emit(prefix, sub)
	return nil
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
		c.stage(uint64(c.d.valType))
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
		return c.d.decodeHeapType(r)
	case immBlockType:
		// The blocktype as the reference reads it: a non-negative type index, the empty
		// form, or a valtype (see decodeBlockType). Staged raw rather than normalized —
		// mapping a valtype blocktype to a synthetic single-result functype is the
		// *validator's* arity computation, and doing it here would put #9's work in the
		// decoder under a name that hides it.
		bt, err := c.d.decodeBlockTypeValue(r)
		if err != nil {
			return err
		}
		c.stage(bt)
		return nil
	case immVecValType:
		// select's optional result-type vector. The types are read and dropped: nothing
		// consumes them, `select` needs its arity from the *stack* at validation time, and
		// a vector does not fit two words (#7).
		return c.d.decodeVec(r, c.d.decodeValType)
	case immVecIdx:
		// br_table's label vector. Not retained — a label list is unbounded, so it cannot
		// live in Instr, and the place it belongs is a side array indexed by instruction
		// once br_table has a consumer (#7). Declared here rather than discovered later.
		return c.d.decodeVec(r, discardIndex)
	case immCatchVec:
		// try_table's handler clauses, likewise unbounded and likewise EH-gated (#7).
		return c.d.decodeVec(r, decodeCatch)
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
	// **Offset in Imm0, memory index in Imm1** — and the alignment is *not* retained.
	// Alignment is a validation constraint (it must not exceed the access's natural
	// width) and carries no execution semantics, so keeping it would be storing a fact
	// only #9 reads, in the two words the interpreter needs. The flags byte is still
	// checked here; what is dropped is only its retention.
	c.stage(off)
	c.stage(memIdx)
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

// decodeCatch reads one try_table handler clause (decode.ml:975-981).
func decodeCatch(r *reader) error {
	kind, err := r.byte()
	if err != nil {
		return err
	}
	var idxs int
	switch kind {
	case 0x00, 0x01: // catch, catch_ref: a tag and a label
		idxs = 2
	case 0x02, 0x03: // catch_all, catch_all_ref: a label
		idxs = 1
	default:
		return fmt.Errorf("%w: %#02x", ErrMalformedCatch, kind)
	}
	for range idxs {
		if err := discardIndex(r); err != nil {
			return err
		}
	}
	return nil
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
	_, err := d.decodeBlockTypeValue(r)
	return err
}

// decodeBlockTypeValue is decodeBlockType returning what it read, encoded so a consumer
// can tell the three forms apart in one word.
//
// The encoding: a type index `i` is `i`, the empty result type is `blockTypeEmpty`, and a
// valtype `t` is `blockTypeValType | uint64(t)`. Three disjoint ranges rather than a
// struct, because this has to fit an Instr immediate — and disjoint by construction, not
// by luck: a type index is `s33` and therefore below 2^32 when non-negative, so the two
// tag bits sit above every legal index.
//
// The alternation writes it through a Decoder field for the reason decodeValType does:
// `either` takes `func(*reader) error` branches and cannot return a value from the one
// that matched.
func (d *Decoder) decodeBlockTypeValue(r *reader) (uint64, error) {
	err := either(r,
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
			d.blockType = uint64(v)
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
			d.blockType = blockTypeEmpty
			return nil
		},
		func(r *reader) error {
			if err := d.decodeValType(r); err != nil {
				return err
			}
			d.blockType = blockTypeValType | uint64(d.valType)
			return nil
		},
	)
	return d.blockType, err
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
	c := &instrCtx{d: d, nonConst: -1, out: &body}
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
	d.mod().Funcs = append(d.mod().Funcs, Func{Locals: locals, Body: body})
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
// It returns the **flattened** local vector — one entry per local, not one per declared
// group — and the flattening happens only after the sum check has passed. That order is
// forced rather than tidy: a body may legally declare 0xFFFFFFFF locals in *encoding*
// while being rejected by `too many locals`, so flattening first would allocate four
// billion entries for a module the next line refuses. binary.wast:159 and :175 are that
// vector.
func (d *Decoder) decodeLocals(r *reader) ([]ValType, error) {
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	type group struct {
		count uint32
		vt    ValType
	}
	var (
		groups []group
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
		groups = append(groups, group{count, d.valType})
	}
	if total >= 1<<32 {
		return nil, fmt.Errorf("%w: %d", ErrTooManyLocals, total)
	}
	locals := make([]ValType, 0, total)
	for _, g := range groups {
		for range g.count {
			locals = append(locals, g.vt)
		}
	}
	return locals, nil
}
