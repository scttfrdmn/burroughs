package interp

import (
	"fmt"
	"math"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// maxElems32 is the element cap for an i32-indexed table — `table.ml:21`'s `valid_size`, which
// is `I64.le_u i 0xffff_ffffL` for I32AT and unconditionally true for I64AT.
//
// **Not memory's `maxPages32`, and the difference is the unit rather than the number.** A
// memory's cap is 0xffff *pages* because 65536 pages is one byte past the largest i32 address;
// a table's is 0xffff_ffff *elements*, the largest i32 index itself, because a slot is one
// element and not 64 KiB. Copying the neighbour's constant across would have capped every table
// at 65535 entries — a wrong answer about a module the spec accepts, in the accept direction,
// and therefore invisible to a rejection corpus by construction. Read off the reference rather
// than reasoned from the sibling, which is the read-the-sibling rule's other half: read it to
// learn the shape, not to borrow its numbers.
const maxElems32 = 0xffff_ffff

// The three reference opcodes, named for control.go's reason: a bare 0xd0 in a switch arm is a
// byte, and these are a family. `opRefNull`/`opRefFunc` are also the two an element expression
// can hold; `opRefIsNull` joins them here rather than getting a second, one-entry block, since
// all three answer to the same "which byte is this" question and a reader checking one checks
// all three for free.
//
// **A third copy of a fact `internal/binary`'s generated table already holds**, so it takes the
// same treatment `opSelect`/`opSelectT` took rather than a fresh justification:
// TestElemExprOpcodesAgreeWithTheDecoder asks the decoder — the authority both copies derive
// from — and discriminates by the immediate, which is what makes a swapped pair fail. Sharing
// instead would mean exporting an opcode set from `binary` for three bytes, putting a table in
// the load-bearing spot for a three-line consumer.
const (
	opRefNull   = 0xd0
	opRefIsNull = 0xd1
	opRefFunc   = 0xd2
)

// trapOOBTable is the spec's out-of-bounds text for a table — `eval.ml:23`'s `table_error`,
// `Table.Bounds -> "out of bounds table access"`.
//
// A different string from memory's, which is why it is a second var rather than a reuse of
// trapOOB: an active element segment that overruns says *table*, and `assert_trap` matches the
// text verbatim.
var trapOOBTable = &Trap{Reason: "out of bounds table access"}

// The two traps a table *access* produces, and they are functions rather than vars because the
// reference appends the index to both: `eval.ml:123-129` is
// `Trap.error at ("undefined element " ^ Int64.to_string i)`.
//
// **The index is part of the string, and one vector proves it belongs there.** Of the 3597
// expectations in the corpus, 3596 stop at the sentinel — but `bulk.wast:222` wants
// `"uninitialized element 2"`, so for that one the rendering is *oracle-covered* (#38's
// refinement: some expected strings carry data). Since the harness matches by substring,
// appending the index passes all 3597 and omitting it fails exactly that one. The suite settles
// it; measuring the population rather than reading one line is what turned a stylistic question
// into a decided one.
//
// undefinedElem is the out-of-bounds access — the table has no such slot — and
// uninitializedElem is an in-bounds slot holding null. Two events, two strings, and collapsing
// them would be the engine's testimony disagreeing with the suite about which happened.
func undefinedElem(i uint64) *Trap {
	return &Trap{Reason: fmt.Sprintf("undefined element %d", i)}
}

func uninitializedElem(i uint64) *Trap {
	return &Trap{Reason: fmt.Sprintf("uninitialized element %d", i)}
}

// table is one table: its slots and the type that bounds them.
//
// **A flat `[]ref` grown by reallocation**, which is `table.ml`'s own shape (`create` makes an
// array of a fill value, `grow` allocates and blits) — the same non-decision memory's flat
// `[]byte` is, and for the same §1 reason: the thesis workload wants a steady state with no
// indirection per access.
//
// The element type is `ref`, the struct 0002 pinned before it had a consumer, and this is that
// consumer arriving. A `[]uint32` of function indices would have been smaller and would have
// conflated `ref.null func` with function 0 — precisely the fact `ref.Null` exists to carry, and
// precisely the distinction `uninitialized element` versus a successful call turns on.
type table struct {
	// slots is the table's contents. Its length is the current size — the reference reads
	// `size` back out of the array (`table.ml:36-37`) rather than keeping a counter, and a
	// second place holding the same fact is how the two drift.
	slots []ref

	// limits is the declared type, kept for the same reason memory.limits is: `grow` needs the
	// max and the index width to decide whether a delta is legal, and `table.grow` is the next
	// opcode to want it.
	limits binary.Limits

	// elemType is the table's declared element type, kept so link (#164) can compare an
	// importer's declared reftype against what the supplier actually built — a funcref table
	// offered where an externref one was declared is `incompatible import type`, and nothing
	// inside the slots (every one starts null) can tell the two apart.
	elemType binary.ValType
}

// newTable allocates a table at its declared minimum, every slot null.
//
// It reports `alloc`'s two failures separately, as `table.ml:30-34` does: SizeOverflow when the
// minimum exceeds the index width's cap, Type when the limits are inverted. Only the first is
// reachable as a trap — inverted limits are #9's verdict, so that arm returns the layering debt,
// exactly as newMemory does one file over.
func newTable(t binary.Table) (*table, error) {
	lim := t.Limits
	if !validTableSize(lim, lim.Min) {
		// `table size overflow` — `eval.ml:24`. A trap, not a verdict: the module said a
		// number the format allows and the index width does not.
		return nil, &Trap{Reason: "table size overflow"}
	}
	if lim.HasMax && lim.Min > lim.Max {
		return nil, fmt.Errorf("%w: table declares min %d above max %d",
			ErrNotValidated, lim.Min, lim.Max)
	}
	if lim.Min > math.MaxInt/refSize {
		return nil, &Trap{Reason: "out of memory"}
	}
	// **Null-filled, and the fill is load-bearing rather than a Go-zero coincidence.** The
	// reference's `create lim.min r` takes the fill value from the table's *initializer*, and
	// for the plain (non-0x40) table form `decode.ml:1063` synthesizes that initializer as
	// `RefNull ht`. So a fresh table's slots are not "empty" — they hold a value, and reading
	// one is `uninitialized element`, not an out-of-bounds access. `ref`'s zero value is
	// `{Null: false, Addr: 0}`, which is *function 0*, so leaving Go's zeroing to speak here
	// would fill every new table with references to the module's first function.
	//
	// The 0x40 form's initializer may be any const-expr and is **not retained** by the decoder
	// (sections.go decodeTableForm says so, and that form is GC-gated), so no accepted module
	// on the default board reaches here wanting a non-null fill.
	slots := make([]ref, lim.Min)
	for i := range slots {
		slots[i] = ref{Null: true}
	}
	return &table{slots: slots, limits: lim, elemType: t.ElemType}, nil
}

// refSize bounds one slot's size in bytes for the allocation check above. Named so the bound
// reads as a bound rather than as arithmetic on a literal.
const refSize = 8

// validTableSize is `table.ml:21`'s `valid_size`: an i32-indexed table is capped at 0xffff_ffff
// elements, an i64-indexed one is not capped here at all.
//
// The i64 arm returning true unconditionally is the reference's, not an omission — a table64's
// size is bounded by what the host can allocate, which is why newTable checks MaxInt separately.
func validTableSize(lim binary.Limits, n uint64) bool {
	if lim.Addr64 {
		return true
	}
	return n <= maxElems32
}

// size is the table's size in elements, read from the backing slice rather than from a counter
// (`table.ml:36-37`).
func (t *table) size() uint64 { return uint64(len(t.slots)) }

// load reads slot i for `call_indirect`'s dispatch, trapping `undefined element i` when it is
// out of bounds — `any_ref`'s wrapper (`eval.ml:122-124`), which is the string only `func_ref`
// (line 274, `call_indirect`'s own resolution) ever produces. It does **not** trap for a null
// slot; `func_ref` is the one that turns a `NullRef` into `uninitialized element`
// (`eval.ml:126-129`), one layer up from here, which is why this function's own job stops at
// "in bounds or not".
//
// **`table.get` does NOT call this**, despite an earlier version of this comment claiming it
// did. `TableGet` (`eval.ml:353-357`) calls `Table.load` — the same runtime primitive `load`
// here models — but catches its bounds exception through `table_error`, which is `"out of
// bounds table access"`: a different string from the same failure, because the reference
// wraps one shared primitive differently at each call site rather than sharing a wrapper. `get`
// below is that second wrapper.
func (t *table) load(i uint64) (ref, error) {
	if i >= t.size() {
		return ref{}, undefinedElem(i)
	}
	return t.slots[i], nil
}

// get reads slot i for `table.get`, trapping `out of bounds table access` when it is out of
// bounds — `Table.load`'s `Bounds` through `table_error`, the wrapper `load` above is not.
// Unlike `call_indirect`'s dispatch, `table.get` returns a null slot as a value rather than
// resolving it further, so there is no second question for a `func_ref`-shaped wrapper to ask.
func (t *table) get(i uint64) (ref, error) {
	if i >= t.size() {
		return ref{}, trapOOBTable
	}
	return t.slots[i], nil
}

// store writes r into slot i, trapping `out of bounds table access` when i is out of bounds —
// `table.ml:69-73`'s `store`, whose `Bounds` maps through `table_error` to that string (the same
// sentinel `blit`'s overrun uses, since both are the same exception).
//
// **No reftype check against the table's declared element type.** The reference's `store` also
// raises `Type` when the value's reftype does not match the table's (`Match.match_reftype`), but
// that is a static fact the validator has already agreed on every accepted module (#9) — nothing
// this decoder admits can reach here with a mismatched type, so enforcing it again would assert a
// property no vector can falsify wrong. `Table.Type`'s only reachable caller in the reference is
// `store`'s own guard; `load` and `blit` have no such check because *they* never receive a value
// to compare.
func (t *table) store(i uint64, r ref) error {
	if i >= t.size() {
		return trapOOBTable
	}
	t.slots[i] = r
	return nil
}

// grow appends delta slots filled with r, reporting the pre-growth size or -1 on failure —
// `table.ml:50-58`'s `grow`, `memory.grow`'s twin one file over and for the identical reason:
// `table.grow` does not trap, it reports failure in the result, so SizeOverflow/SizeLimit become
// -1 here rather than errors. Returning an error would turn every failed grow into a trap and
// answer `table_grow.wast`'s negative rows with the wrong verdict.
//
// **`limits.Min` grows with the table, for `memory.grow`'s reason exactly** (memory.go's own
// comment): `type_of` — read at import-match time — must see the *current* size, or a table
// grown and then re-exported reports its stale pre-growth minimum to an importer whose
// declaration matches reality. `table_grow.wast`'s own corpus vectors are the sibling of
// `imports4.wast`'s memory case, not yet measured because this arm did not exist to grow
// anything for them to see.
func (t *table) grow(delta uint64, r ref) int64 {
	old := t.size()
	newSize := old + delta
	if newSize < old { // 64-bit wrap: `I64.gt_u old_size new_size`
		return -1
	}
	if !validTableSize(t.limits, newSize) {
		return -1
	}
	if t.limits.HasMax && newSize > t.limits.Max {
		return -1
	}
	if newSize > math.MaxInt/refSize {
		return -1
	}
	grown := make([]ref, newSize)
	copy(grown, t.slots)
	for i := old; i < newSize; i++ {
		grown[i] = r
	}
	t.slots = grown
	t.limits.Min = newSize
	return int64(old)
}

// fill writes r into n consecutive slots starting at i, trapping `out of bounds table access`
// when the run does not fit — `table.ml:80-84`'s bound, the same `blit` already checks, stated
// directly rather than filling and catching an overrun mid-loop. `TableFill`'s reference
// (`eval.ml:375-392`) is a recursive store-then-recurse; a loop over the reference's own bound
// check is the same effect without staging n synthetic instructions to re-enter this opcode.
func (t *table) fill(i, n uint64, r ref) error {
	end := i + n
	if end < i || end > t.size() { // `lt_u (add i n) i || gt_u (add i n) j`
		return trapOOBTable
	}
	for k := i; k < end; k++ {
		t.slots[k] = r
	}
	return nil
}

// blit stores rs at offset, trapping `out of bounds table access` when the run does not fit —
// `table.ml:75-79`, whose bound is `offset > length - len`.
//
// **Written as the addition form, which is the reference's own test one layer up rather than a
// transcription of this one.** `table.ml`'s subtraction is safe in OCaml because `Int64.sub`
// yields a negative and the comparison is signed; the same expression on uint64 wraps to a huge
// number and would admit every overrun. `eval.ml:159`'s `oob i n j` is
// `lt_u (add i n) i || gt_u (add i n) j` — the wrap check and the extent check, in that order —
// and that is what this is. An empty run at exactly the end stays in bounds either way, which
// is the case the two forms are most easily assumed to differ on.
func (t *table) blit(offset uint64, rs []ref) error {
	end := offset + uint64(len(rs))
	if end < offset || end > t.size() { // `lt_u (add i n) i || gt_u (add i n) j`
		return trapOOBTable
	}
	copy(t.slots[offset:], rs)
	return nil
}

// tableFor resolves a table index to a table. It is the *only* place that does, which is what
// keeps its two failure modes from being half-remembered elsewhere — memoryFor's rule, and the
// reason that one exists as a named function rather than as an index expression per site. The
// grave it was paid for is #78/#105/#106's shape: two places knowing how to turn an index into a
// thing.
//
// `what` names the construct holding the index — "instruction", "element segment" — because the
// error is read by someone looking for it in their module, and "instruction names table 3 of 2"
// sends them to the wrong line when the index was in an `(elem (table 3) …)`.
func (in *Instance) tableFor(what string, idx uint64) (*table, error) {
	if idx >= uint64(len(in.tables)) {
		return nil, fmt.Errorf("%w: %s names table %d of %d",
			ErrNotValidated, what, idx, len(in.tables))
	}
	if in.tables[idx] == nil {
		// A reserved slot with nothing in it, and the two reasons are reported apart because
		// they are different facts about the engine — memoryFor's split, and discriminated the
		// same way, by the *import offset* rather than by whether `deferred` happens to be set.
		// Below the offset is an imported table nothing supplied, where nothing went wrong with
		// the module; above it, a declared table whose allocation failed for a verdict-shaped
		// reason, quoted rather than paraphrased. The *logic* is unchanged by linking arriving,
		// for the reason memoryFor states at length: a filled slot never reaches here. The
		// message changed for memoryFor's other reason — it claimed the engine has no linker.
		if idx < uint64(in.mod.ImportedTables()) {
			return nil, fmt.Errorf("%w: table %d is an import nothing supplied (contract §3)",
				ErrUnsupported, idx)
		}
		return nil, fmt.Errorf("%w: table %d was declared but not allocated: %w",
			ErrNotValidated, idx, in.deferred)
	}
	return in.tables[idx], nil
}

// runElem performs one element segment's instantiation-time effect — `run_elem`
// (`eval.ml:1264-1277`), whose three modes emit three different instruction sequences:
//
//	Passive      -> []
//	Active (y,c) -> c; i32.const 0; i32.const len; table.init y x; elem.drop x
//	Declarative  -> elem.drop x
//
// So **two of the three modes drop, and only Passive survives instantiation with contents.** The
// copy is `blit` rather than a literal `table.init` because the operands are known here — offset
// from the segment's own const-expr, source 0, length the whole segment — and staging three
// constants onto a stack to re-derive them would be transcription over translation. What is *not*
// a liberty is the drop: it is an observable state change, and `bulk.wast:250-270` is the vector
// that observes it (see segment.go).
//
// The earlier shape of this function returned early for the two non-active modes and modelled no
// drop at all, with a comment saying so and citing #7. That deferral is discharged here: the drop
// is the reason `table.init` cannot land without it.
func (in *Instance) runElem(idx int, seg *binary.ElemSegment) error {
	if seg.Mode == binary.ElemPassive {
		return nil
	}
	// **A trapping copy does not drop, and the ordering below is the reference's rather than a
	// convenience.** The drop is a *later instruction* in `run_elem`'s sequence, so a trapping
	// `TableInit` aborts before it. Whether that is observable is a separate question with a
	// measured answer: through this module's own exports it is not, because the trap propagates
	// out of instantiation and the instance never runs — but `linking.wast:413` is the pattern
	// where a failed instantiation's *earlier* side effects are asserted from the next command, so
	// the class is observable and this one is not distinguished from it by inspection.
	inst, err := in.elemFor("element segment", uint64(idx))
	if err != nil {
		return err
	}
	if seg.Mode == binary.ElemActive {
		tab, err := in.tableFor("element segment", uint64(seg.TableIndex))
		if err != nil {
			return err
		}
		off, err := in.constAddr(seg.Offset, "an element segment's offset")
		if err != nil {
			return err
		}
		// Offset 0 with an empty segment is in bounds for a zero-length table, which blit gets
		// right for free — the same freebie write gets, and for the same reason.
		if err := tab.blit(off, inst.refs); err != nil {
			return err
		}
	}
	inst.drop()
	return nil
}

// segmentRefs evaluates a segment's elements to reference values, in whichever of the two forms
// the wire used.
//
// The two arms are what ElemSegment.ByExpr keeps apart, and they are genuinely different work
// rather than two spellings of one: the index form names a function directly, while the
// expression form must evaluate a const-expr that may be `ref.null`. Collapsing them would mean
// either synthesizing a `ref.func` expression per index or pattern-matching expressions back
// into indices — and the second is the normalization 0016 records that this engine *cannot* do.
// `binary.ValType` gained a nullability bit under 0018, but that alone does not revive the
// normalization: recovering `is_elem_kind` needs the *segment's* element type (now
// representable) *and* a check over every expression in `Exprs` ("is each one exactly
// `[ref_func x]`?", encode.ml:1052-1054), which is a property of the segment's content, not
// something a single ValType comparison decides. ByExpr's decode-time record of which grammar
// arm ran remains the simpler and already-correct source of truth.
func (in *Instance) segmentRefs(seg *binary.ElemSegment) ([]ref, error) {
	if !seg.ByExpr {
		rs := make([]ref, len(seg.Funcs))
		for i, x := range seg.Funcs {
			rs[i] = ref{Addr: x, Inst: in}
		}
		return rs, nil
	}
	// **The element type is checked to be a reftype before it is used as one**, even though
	// `ElemType`'s own doc comment says every wire form yields one. The check is the difference
	// between a guard and a comment: `constExpr` dispatches on the type it is handed, so a
	// numeric `ElemType` would ask for a numeric slot, leave `constVal.ref` at its zero value,
	// and fill the table with references that are neither null nor valid — an accept-direction
	// wrong answer, which is the direction §9 G-3 says the suite scores green by construction.
	// Unreachable through the decoder today; scoped to the space, not to the sample.
	if !seg.ElemType.IsRef() {
		return nil, fmt.Errorf("%w: an element segment declares element type %s, which is not a "+
			"reference type", ErrNotValidated, seg.ElemType)
	}
	rs := make([]ref, len(seg.Exprs))
	for i := range seg.Exprs {
		v, err := in.constExpr(seg.Exprs[i], seg.ElemType, "an element expression")
		if err != nil {
			return nil, err
		}
		rs[i] = v.ref
	}
	return rs, nil
}

// `constExprRef` used to live here: a two-pattern matcher over `[ref.null, END]` and
// `[ref.func x, END]` that reported every other element expression `ErrUnsupportedOp` by name. It
// is **deleted** rather than moved (#241), its own pre-registered retirement condition having been
// met by #172's rung 1 — see constexpr.go's header for why a matcher is not a subset of the
// evaluator that replaced it, and `leadingOp`, which existed only to render that error, went with
// it.
//
// The retention gap it declared did *not* go with it, and it is now **closed** rather than merely
// re-homed. It said `immHeapType` stages no word, so `ref.null func` and `ref.null extern` decode
// to identical `Instr`s; that was true until #359, which files the reftype in `Func.Casts` instead
// of in a word — the side table 0027 built for the cast family, which costs `immStagedBits` nothing.
// The owner it named was **#8**, the encoder; the consumer that actually arrived is **#9**'s
// validator, and the reason both are static owners and no interpreter arm is one is unchanged
// (rung 5's casts asked and got `BotHT`, `value.ml:112`). The full account is at `opRefNull`'s arm
// in exec.go, which is where a reader of a null's static type will be standing.
