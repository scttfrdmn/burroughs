//go:build burroughs_endtable

package binary

// This file is the block-pairing table: the gated half of 0002's Q1 option B, and #136's
// mechanism.
//
// The default build's twin is endtable_disabled.go, and the split is deliberately as thin
// as it can be — one struct field, four hooks, all of them no-ops over there. Everything
// that is *not* build-dependent lives in the shared files: `Func.EndsOff` is declared
// unconditionally because 0048 measured it free (it sits in `TypeIndex`'s padding), and
// `structural`'s two call sites are unconditional because a call to an empty method
// compiles to nothing. What a tag has to be able to remove is the 24-byte slice header per
// module and the decode-time work, and that is all this file's twin removes.

// moduleEnds holds one module's pairing arena: every defined body's table, concatenated,
// each `len(Body)` slots wide and located by that body's `Func.EndsOff`.
//
// # One arena rather than a slice per function, and the reason is a measured bill (0048)
//
// The obvious shape is `Ends []int32` on `Func`. It loses, and not narrowly: a slice
// header is 24 bytes on every one of the corpus's 9393 functions whether or not the body
// opens a block, and 86.5% of them do not. The three placements 0048 priced over the same
// corpus, cheapest first — this one at 154520 B, one pointer per function at 160240 B, an
// inline slice header at 280120 B, against 324280 B for 0016's map. The arena wins because
// its per-function cost is a single `int32` that the layout was already wasting.
//
// The extent is not stored. It is `len(Body)`, which the reader has in hand, so the table
// is a subslice and needs no second field — and needs it *badly*, because `Func` has room
// for exactly one free `int32` and this representation spends it on the offset.
type moduleEnds struct {
	ends []int32
}

// endPair is one recorded pairing, in decode order rather than index order: `structural`
// recurses, so an inner block's END is known before its enclosing block's.
type endPair struct{ pc, end int32 }

// decoderEnds is the per-decode scratch: the pairings of the body currently being read.
//
// On the Decoder rather than the instrCtx so that it is reused across the code section's
// bodies — one buffer per decode instead of one per function. That reuse is what makes it
// scratch, and scratch that survives a measurement reports history (#28), so
// `beginFuncEnds` truncates it at the *start* of every body rather than the end of the
// last one: a body that fails mid-grammar leaves its pairs behind, and the next body would
// otherwise file them as its own.
type decoderEnds struct {
	pairs []endPair
}

// maxEndsBase is the largest arena offset the biased `int32` in `Func.EndsOff` can name.
//
// Unreachable at any module size that exists — 2^31 slots is 48 GiB of `Instr` in bodies
// alone — and checked anyway, because the alternative to a check is a silent wrap that
// would point a body at another body's table. Over the bound the function keeps
// `EndsOff == 0` and the interpreter scans, which is the default build's behaviour and so
// is known to be correct.
const maxEndsBase = 1<<31 - 2

// openerAt returns the index the next emitted instruction will take, or −1 when nothing is
// being retained (the const-expression path reads for a verdict and emits nowhere).
func (c *instrCtx) openerAt() int32 {
	if c.out == nil {
		return -1
	}
	return int32(len(*c.out))
}

// pairEnd records that the header at `opener` is closed by the END just emitted.
//
// Called from `structural` after `endTerminator`, which is the only place both indices are
// known at once: the header's index is taken before the header is emitted and the
// terminator's is the last thing in the sequence. That is the same discipline `emit` uses
// for the side tables — take the index before the append, not after.
func (c *instrCtx) pairEnd(opener int32) {
	if opener < 0 {
		return
	}
	c.d.pairs = append(c.d.pairs, endPair{pc: opener, end: int32(len(*c.out) - 1)})
}

// beginFuncEnds resets the scratch for a new body. See decoderEnds on why this is the
// start of a body and not the end of one.
func (d *Decoder) beginFuncEnds() { d.pairs = d.pairs[:0] }

// fileFuncEnds expands the scratch into the module arena and points the just-appended Func
// at it.
//
// **Precondition: the Func is already in `m.Funcs`.** It reads the body's length from
// there rather than taking it as an argument, so a caller that files before appending
// would file against the previous function — and the call site is one line below the
// append for exactly that reason.
//
// Filed here, past every verdict `decodeFuncBody` can return, for the reason the body's
// own retention is: a module the decoder is about to reject must not leave a table behind.
func (d *Decoder) fileFuncEnds() {
	if len(d.pairs) == 0 {
		return
	}
	m := d.mod()
	f := &m.Funcs[len(m.Funcs)-1]
	base := len(m.ends)
	if base > maxEndsBase-len(f.Body) {
		return
	}
	// −1 is the no-header slot, and the table is dense: it is indexed by instruction, so
	// every slot the body has must exist even though 74.4% of them describe no header.
	// 0048 charged that density against the two sparse alternatives and it is still the
	// cheapest, because the sparseness has to be paid for somewhere and a map pays ~197 B
	// of header per function that has one.
	for range len(f.Body) {
		m.ends = append(m.ends, -1)
	}
	tbl := m.ends[base:]
	for _, p := range d.pairs {
		tbl[p.pc] = p.end
	}
	f.EndsOff = int32(base + 1)
}

// trimEnds copies the arena down to its exact extent.
//
// `append` grows geometrically, so the arena as built carries up to its own length again
// in slack — and 0048's bill was measured on exact-size arenas, which makes the trim part
// of what was priced rather than a tidiness. Called after `finishFuncs`, which is also
// where a count disagreement truncates `m.Funcs`: the tables of the dropped functions stay
// in the arena, unreachable and unsummed, which is the same trade `finishFuncs` documents
// for dropping the functions themselves.
func (d *Decoder) trimEnds() {
	m := d.mod()
	if cap(m.ends) > len(m.ends) {
		m.ends = append([]int32(nil), m.ends...)
	}
}

// FuncEnds returns f's block-pairing table: slot `i` is the index of the END closing the
// header at `f.Body[i]`, or −1 where no header opens there. It returns nil when there is
// no table — the body opens no block, or this is the default build.
//
// **No bounds guard, and that is not an omission.** The arena has exactly one writer, in
// this file, which appends `len(f.Body)` slots and files the offset in the same breath; an
// offset that does not fit is an engine bug and no guest input can produce one. A guard
// here could only convert that bug into a silent nil, which reads as "this body opens no
// block" and makes the interpreter fall back to scanning — correct output, no signal, and
// the pairing table quietly not working. The range check that is already there is louder.
func (m *Module) FuncEnds(f *Func) []int32 {
	if f.EndsOff <= 0 {
		return nil
	}
	base := int(f.EndsOff) - 1
	return m.ends[base : base+len(f.Body)]
}
