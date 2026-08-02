package binary

import (
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
// import, terminated by END. Extended-const (`i32.add` and friends) and WasmGC
// (`struct.new`) widen it, and both are gated, so they arrive with their gates.
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
	c := &instrCtx{d: d, constOnly: true, nonConst: -1}
	if err := c.block(r); err != nil {
		return err
	}
	if err := expectEnd(r); err != nil {
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
	if !c.constOnly || constOps[b] {
		return c.imms(r, info.imms)
	}
	// Deferred, not returned: see decodeConstExpr. The *first* one is kept, because
	// that is the one a validator reading left to right would report.
	if c.nonConst < 0 {
		c.nonConst = int(b)
	}
	return c.imms(r, info.imms)
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
	if err := c.imms(r, info.imms); err != nil {
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

// imms reads an immediate sequence in the table's order.
//
// Order is the fact the table exists to carry: a wrong order shifts every subsequent
// byte, and that surfaces somewhere else entirely rather than here.
func (c *instrCtx) imms(r *reader, ims []imm) error {
	// The structural arms, keyed off immBlock's presence rather than off an opcode
	// list — the marker is the shape, so a fifth such arm upstream is caught by
	// TestStructuralArmsAreExactlyTheBlockRows instead of being read flat.
	for _, im := range ims {
		if im == immBlock {
			return c.structural(r, ims)
		}
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
func (c *instrCtx) structural(r *reader, ims []imm) error {
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
			if err := c.block(r); err != nil {
				return err
			}
		}
	}
	return expectEnd(r)
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
		_, err := r.u32()
		return err
	case immS32:
		_, err := r.s32()
		return err
	case immS64:
		_, err := r.s64()
		return err
	case immF32:
		_, err := r.bytes(4)
		return err
	case immF64:
		_, err := r.bytes(8)
		return err
	case immV128:
		_, err := r.bytes(16)
		return err
	case immByte:
		_, err := r.byte()
		return err
	case immLaneIdx:
		// `laneidx s = u8 s = uN 8` (decode.ml:152,103) — a LEB whose canonical form is
		// one byte and whose legal form runs to two. Grave #47 read it as a raw byte.
		_, err := r.uleb(8)
		return err
	case immLane16:
		// `repeat 16 laneidx s` (decode.ml:699): sixteen laneidx reads, so 16..32 bytes.
		for range 16 {
			if _, err := r.uleb(8); err != nil {
				return err
			}
		}
		return nil
	case immValType:
		return c.d.decodeValType(r)
	case immHeapType:
		// Partial, and declared so: `heaptype` is `either [typeuse s33; s7 ...]` over
		// twelve shorthands, where decodeRefType covers the two this decoder accepts.
		// Widening it is the GC gate's business, not this PR's — see the immHeapType
		// entry in immBytes, which carries the same bound.
		return c.d.decodeRefType(r)
	case immBlockType:
		return c.d.decodeBlockType(r)
	case immVecValType:
		return c.d.decodeVec(r, c.d.decodeValType)
	case immVecIdx:
		return c.d.decodeVec(r, discardIndex)
	case immCatchVec:
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
	if flags&0x40 != 0 { // bit 6 selects an explicit memory index
		if !c.d.Features.MultiMemory {
			c.decline(featureErr(gatedNonOpcodes[gateMultiMemory]))
		}
		if err := discardIndex(r); err != nil {
			return err
		}
	}
	return discardMemopOffset(r)
}

// discardMemopOffset reads a memarg's offset, which is a **u64** — `memop` ends with
// `let offset = u64 s` (decode.ml:332).
//
// Its own function so the width has a definition site rather than only a call site. The
// difference is two vectors: at binary.wast:730 a ten-byte offset with unused bits set
// wants `integer too large`, which a u32 read reports as `too long`. Reading it as a u32
// refilled five vectors when probed.
func discardMemopOffset(r *reader) error {
	_, err := r.u64()
	return err
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
// with `malformed value type` — a gate manufacturing malformedness for a construct Wasm
// 3.0 defines. Pinned by TestBlockTypeAlternationIsTheAuthority, whose doc records the
// wrong reason this comment used to give.
func (d *Decoder) decodeBlockType(r *reader) error {
	return either(r,
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
				return ErrMalformedValType
			}
			return nil
		},
		d.decodeValType,
	)
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
	if err := d.decodeLocals(r); err != nil {
		return err
	}
	c := &instrCtx{d: d, nonConst: -1}
	if err := c.block(r); err != nil {
		return err
	}
	if err := expectEnd(r); err != nil {
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
	return c.release()
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
func (d *Decoder) decodeLocals(r *reader) error {
	n, err := r.u32()
	if err != nil {
		return err
	}
	var total uint64
	for range n {
		count, err := r.u32()
		if err != nil {
			return err
		}
		if err := d.decodeValType(r); err != nil {
			return err
		}
		total += uint64(count)
	}
	if total >= 1<<32 {
		return fmt.Errorf("%w: %d", ErrTooManyLocals, total)
	}
	return nil
}
