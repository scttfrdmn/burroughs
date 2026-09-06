package interp

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"unsafe"

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
// refinement: some expected strings carry data). Since the harness matches by prefix (ADR 0045)
// and the sentinel is at position 0, appending the index passes all 3597 and omitting it fails
// exactly that one. The suite settles
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

// tabImage is the published header of a table's slots — [decision 0065][0065]'s transfer of
// [0058][0058]'s `memImage` to this subject, and the two comments there carry over without
// alteration: the *contents* are written all the time, the three words naming the array are written
// once before the descriptor is reachable and never again, and it is a struct rather than an
// `atomic.Pointer[[]ref]` because the type is where the immutability is stated.
//
// **The field lives here and nowhere else, which is 0065's own increment.** `table` below holds no
// slice, so `t.slots` is a compile error rather than a shape a control has to watch for — the
// difference between a claim that holds by construction and one that holds by a scan (ADR 0064 had
// to buy the second kind).
//
// [0058]: ../../docs/decisions/0058-the-memory-image-is-published-through-an-atomic-pointer-because-reachability-is-not-a-spawn-time-property.md
// [0065]: ../../docs/decisions/0065-the-table-and-segment-headers-move-inside-published-images-because-a-field-that-cannot-be-named-needs-no-enumeration-to-confine-it.md
type tabImage struct {
	// slots is the table's contents. Its length is the current size — the reference reads
	// `size` back out of the array (`table.ml:36-37`) rather than keeping a counter, and a
	// second place holding the same fact is how the two drift.
	slots []ref
}

// table is one table: a published image of its slots and the type that bounds them.
//
// **A flat `[]ref` grown by reallocation**, which is `table.ml`'s own shape (`create` makes an
// array of a fill value, `grow` allocates and blits), behind the one indirection
// [decision 0065][0065] pays for. What §1's workload wants is a table whose *steady state* is a
// single contiguous slice, which this is; the sentence here used to add *"with no indirection per
// access"* and 0065 falsifies that half exactly as 0058 falsified memory's, so it is removed rather
// than left for a reader to trust.
//
// **A grow reslices into a reservation where the module declared a max, and relocates otherwise**, which
// is [decision 0075][0075] replacing 0065's decision 6. That paragraph read *"every grow relocates, and
// there is no `noMove` mark … nothing addresses a table atomically, and the threads proposal at this pin
// has no shared tables, so there is no reservation to reslice into"* — the two premises were about
// *atomics* and *sharing*, and the defect they missed needs neither: an agent holding an older image writes
// into the array a relocation abandons, and its write is lost
// ([#662](https://github.com/scttfrdmn/burroughs/issues/662)). So the conclusion is falsified while both of
// its premises stand, and one half of it survives: there is still **no `noMove` mark**, because the whole
// of what a stranded table agent loses is plain writes and `ws` below answers that case exactly, where a
// memory's atomics need the categorical refusal a mark buys (0075, decision 5).
//
// [0075]: ../../docs/decisions/0075-a-table-reserves-to-its-declared-max-under-a-measured-ceiling-and-refuses-to-relocate-with-a-sibling-agent.md
//
// The element type is `ref`, the struct 0002 pinned before it had a consumer, and this is that
// consumer arriving. A `[]uint32` of function indices would have been smaller and would have
// conflated `ref.null func` with function 0 — precisely the fact `ref.Null` exists to carry, and
// precisely the distinction `uninitialized element` versus a successful call turns on.
//
// [0065]: ../../docs/decisions/0065-the-table-and-segment-headers-move-inside-published-images-because-a-field-that-cannot-be-named-needs-no-enumeration-to-confine-it.md
type table struct {
	// img is the current published image. Read it once per operation and use the slice it names for
	// the whole of that operation — **two loads in one bounds-check-then-access pair is the defect
	// this field exists to prevent**, since the second load may name a different array than the one
	// the check approved. `TestEveryOperationLoadsAPublishedImageAtMostOnce` is the control.
	img atomic.Pointer[tabImage]

	// growMu serialises `grow` against `grow`, which is ADR 0061 transferred to this subject with its
	// reason and not only its shape: the length lives in two places — this image's slice length and
	// `limits.Min` below — and only one of them is in the descriptor, so a compare-and-swap over
	// `img` would relocate the defect to the copy it cannot reach. Both writes are inside the
	// section.
	//
	// The order is `growMu` → `relocMu` → `world.mu`…, which this comment said did not exist —
	// *"there is no lock order to get wrong … `grow` is the only taker and it takes nothing else"* —
	// until `relocate` below gave the relocating arm two more locks to take (decision 0075, decision 3).
	// It is memory's order, and it is memory's *mutex*: `relocMu` is process-wide and covers both
	// subjects, so a table relocation and a memory relocation cannot take two worlds' mutexes in
	// opposite orders. `attachWorld` takes this lock too, which is what keeps `ws` off every access path.
	growMu sync.Mutex

	// ws is every world whose index space holds this table — memory's field with the subject swapped,
	// and every paragraph on `memory.ws` carries over: one entry per instance that defines or imports it,
	// deduplicated by identity, empty until an instance installs it, registered by `build` where reach is
	// granted rather than found by a walk, written and read only under `growMu`, append-only.
	//
	// **It is here because `relocate` needs to ask a question a `table` could not ask** (decision 0075):
	// abandoning an image is coherent exactly when no *other* agent could be holding it, which is a
	// question about a world's callers.
	//
	// **The cross-instance case is the ordinary one for tables too, and it was measured for memories
	// before being assumed here**: `memory.ws`' own comment records a first draft that refused every
	// relocation once a second instance appeared and cost 30 default-lane passes, because the spec's own
	// growth fixture exports two memories from one module and imports them into another. `linking.wast`
	// is the table shape of the same fixture, so a slice-and-not-a-bool is transferred with the finding
	// rather than re-derived from a fresh guess.
	ws []*world

	// limits is the declared type, kept for the same reason memory.limits is: `grow` needs the
	// max and the index width to decide whether a delta is legal, and `table.grow` is the next
	// opcode to want it.
	limits binary.Limits

	// elemType is the table's declared element type, kept so link (#164) can compare an
	// importer's declared reftype against what the supplier actually built — a funcref table
	// offered where an externref one was declared is `incompatible import type`, and nothing
	// inside the slots (every one starts null) can tell the two apart.
	elemType binary.ValType

	// mod is the module `elemType`'s type index is read in when it is an indexed reference
	// form. See `global.mod` for why the defining module rides on the allocation rather than on
	// the Extern, and #368 for the four modules the comparison it feeds used to accept.
	mod *binary.Module
}

// newTable allocates a table at its declared minimum, every slot holding the value its initializer
// evaluates to — `init_table` (`eval.ml:1219-1228`) and `Table.alloc`'s `create lim.min r`.
//
// It reports `alloc`'s two failures separately, as `table.ml:30-34` does: SizeOverflow when the
// minimum exceeds the index width's cap, Type when the limits are inverted. Only the first is
// reachable as a trap — inverted limits are #9's verdict, so that arm returns the layering debt,
// exactly as newMemory does one file over.
//
// # The initializer is evaluated before the size checks, which is the reference's order
//
// `init_table` runs `eval_const inst c` and *then* calls `Table.alloc`, whose first two lines are
// `valid_size` and `valid_limits`. So a table that is both too large and has an unevaluable
// initializer reports the initializer. No vector on either lane pairs those two defects — a const
// expression that fails at all is `assert_invalid` territory, so the modules that reach here have
// initializers that work — and the order is transcribed rather than chosen for that reason: an
// ordering with no witness is one to take from the authority, not from whichever branch was already
// written first.
//
// # A method, because the initializer is evaluated against the instance
//
// `eval_const inst c` reads the instance as it stands, exactly as `init_global`'s does, and the fold
// puts `init_table` after `init_global` (`eval.ml:1314-1315`) — so a table initializer can read a
// global this instance has already built. `newGlobal` is a method for the same reason and this is now
// its shape: one call to `in.constExpr`, the want taken from the declared element type, and no branch
// on what kind of value comes back (grave #239's lesson, recorded on `newGlobal`).
//
// The reference's non-reference arm is `Crash.error c.at "non-reference table initializer"` — a
// *crash*, not a trap and not a verdict, because `check_table` has already typed the expression
// against `RefT rt`. `constExpr`'s arity check is this engine's spelling of the same guarantee: it
// asks for one reference slot and reports a mismatch through the deferred channel, which is where a
// validator debt belongs (0015).
func (in *Instance) newTable(t binary.Table) (*table, error) {
	v, err := in.constExpr(t.Init, t.ElemType, "a table initializer")
	if err != nil {
		return nil, err
	}
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
	// **Filled from the initializer, and the explicit fill is load-bearing rather than a Go-zero
	// coincidence.** `ref`'s zero value is `{Null: false, Addr: 0}`, which is *function 0*, so a
	// `make` alone would fill a table with references to the module's first function whatever its
	// initializer said. Even for the plain wire form — whose initializer is the `ref.null ht` the
	// decoder synthesizes (`decode.ml:1058-1063`) — the null has to be written.
	//
	// This paragraph said "every slot null" and named that as the whole story until #419, which was
	// true of what the *decoder retained* rather than of the rule: the 0x40 form's initializer was
	// read and discarded, so there was no other value to fill with. Both halves changed together —
	// the field is retained and this fill reads it — and a fresh table's slots are still not
	// "empty": they hold whatever `v.ref` is, and reading a null one is `uninitialized element`
	// rather than an out-of-bounds access.
	//
	// **The capacity is the declared max under a ceiling; the length is still the declared minimum** —
	// [decision 0075][0075]'s reservation, which is what lets `grow` reslice instead of abandoning an
	// image a sibling agent may be holding (#662). `reserve` never falls below `lim.Min`, so a minimum
	// above the ceiling is allocated in full and simply cannot grow — `allocate`'s arm one file over,
	// reported by `grow` as the spec's `-1`. A table that declared **no** max reserves nothing: the engine
	// reserves what the module declared and never what the engine would guess (0075, decision 1).
	//
	// **The reserved tail is left zeroed here and filled by `grow`, which is the opposite of memory's
	// arrangement and has to be.** A zeroed `ref` is `{Null: false, Addr: 0}` — function 0 — so the tail
	// is not merely uninitialised, it is *wrong* for every fill value; `grow`'s reslicing arm writes `r`
	// across the new slots before publishing the longer image, and filling with `v.ref` here would be a
	// second, staler claim about slots nobody can see yet.
	//
	// **No fourth `math.MaxInt` guard, deliberately.** `reserve` is at most the larger of `lim.Min` —
	// guarded two lines up — and `tableReserveSlots`, which is this package's own constant rather than a
	// module input, so a guard here could not fire for anything a module can declare, and
	// [#635](https://github.com/scttfrdmn/burroughs/issues/635) is already about three guards that cannot.
	//
	// [0075]: ../../docs/decisions/0075-a-table-reserves-to-its-declared-max-under-a-measured-ceiling-and-refuses-to-relocate-with-a-sibling-agent.md
	reserve := lim.Min
	if lim.HasMax {
		reserve = max(lim.Min, min(lim.Max, tableReserveSlots))
	}
	slots := make([]ref, lim.Min, reserve)
	for i := range slots {
		slots[i] = v.ref
	}
	tab := &table{limits: lim, elemType: t.ElemType, mod: in.mod}
	// The first publication, and it happens here rather than lazily in `view` for 0058's reason on
	// `allocate`: a lazily-initialised image would need a nil check on every access, and every path
	// out of this constructor either returns an error above or a table whose image is stored.
	tab.img.Store(&tabImage{slots: slots})
	return tab, nil
}

// refSize is one slot's size in bytes for the allocation checks above and in `grow`. It bounds
// nothing on its own; it is the divisor that turns a slot count into a byte count.
//
// **Derived from the type rather than written down, because a literal here is correct exactly once**
// (grave #621). It read `const refSize = 8`, and that was *true* the day it landed (#148, 2026-08-05):
// `ref` was `{Null bool; Addr uint32}`, eight bytes. Every field added since falsified it silently —
// rung 4 took the struct to 32 and then 40, rung 5 slice 3 added two more bools inside the padding —
// and today a `ref` is nine fields (`Null`, `IsI31`, `Externalized`, `IsHost`, `Addr`, `I31`, `Inst`,
// `Exc`, `Obj`), 40 bytes, five words. So both guards divided by a number five times too small and
// admitted five times the slots the comment said they would.
//
// **The tree recorded every step of that drift beside this constant and no instrument could join
// them.** TestRefWidthIsMeasuredNotAssumed has pinned the real width since #256 (2026-08-12) and its
// own comment narrates each widening; for the twenty-three days between the two commits, and for every
// day after, this package held two numbers for one fact and the control had the right one. A size
// assertion cannot see a divisor, and a divisor cannot see a size assertion — so the repair is not a
// third number but the removal of the second: `unsafe.Sizeof` is a compile-time constant, no pointer
// arithmetic and no runtime cost, and a tenth field on `ref` now moves this divisor instead of
// widening the guard behind it. That the stale copy sat under a sentence *claiming* to state one
// slot's size is the reason review kept passing over it — *a comment asserting the property the code
// lacks makes review confirm the bug*.
//
// **What the correct divisor does and does not buy, stated because the arithmetic error was not the
// whole defect.** With 40 the reachable window closes: a table64 declaring a min between
// `MaxInt/40` and `MaxInt/8` used to pass this check and then die in `make([]ref, n)` with
// `makeslice: len out of range` — a Go panic escaping where the arm two lines up intends
// `&Trap{Reason: "out of memory"}` — and now it traps. That window is the only place either guard
// has ever been able to fire, and it is gated: `validTableSize` caps an i32 table at `maxElems32`,
// which is about 5.4e7 times *below* `MaxInt/40`, so **on the i32 path this arm still cannot fire
// for any input at all.** The bound that path wants is not `MaxInt` but a host-allocation limit —
// `maxElems32` slots is ~172 GB of `ref` — and choosing one is a decision with its own issue
// ([#635](https://github.com/scttfrdmn/burroughs/issues/635)), not something to pick here. Until it
// lands, an i32 table declaring a min near the cap makes a real allocation attempt whose outcome
// this engine does not define. That is named rather than measured: nothing in this tree has run it.
//
// The `uint64` conversion is not decoration: `unsafe.Sizeof` is typed `uintptr`, and both guards
// compare it against a `uint64` slot count, so leaving it `uintptr` is a compile error rather than a
// silent widening. Converting the *constant* keeps the whole expression constant-folded.
const refSize = uint64(unsafe.Sizeof(ref{}))

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

// view is the currently published slot array — `(*memory).view`'s twin, and it carries that name
// deliberately rather than for symmetry: `TestEveryOperationLoadsAPublishedImageAtMostOnce` matches
// on the selector, so naming this `view` puts tables inside the existing load-once control instead
// of asking for a fourth one (0065's decision 2).
//
// Hold the result for the whole of one operation; do not call this twice in one.
func (t *table) view() []ref { return t.img.Load().slots }

// size is the table's size in elements, read from the backing slice rather than from a counter
// (`table.ml:36-37`).
//
// **This is itself an image load**, which is why the control counts `size` alongside `view`: a caller
// that checks a bound against `size()` and then indexes `view()` has taken two loads and may check
// one array while accessing another.
func (t *table) size() uint64 { return uint64(len(t.view())) }

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
	slots := t.view()
	if i >= uint64(len(slots)) {
		return ref{}, undefinedElem(i)
	}
	return slots[i], nil
}

// get reads slot i for `table.get`, trapping `out of bounds table access` when it is out of
// bounds — `Table.load`'s `Bounds` through `table_error`, the wrapper `load` above is not.
// Unlike `call_indirect`'s dispatch, `table.get` returns a null slot as a value rather than
// resolving it further, so there is no second question for a `func_ref`-shaped wrapper to ask.
func (t *table) get(i uint64) (ref, error) {
	slots := t.view()
	if i >= uint64(len(slots)) {
		return ref{}, trapOOBTable
	}
	return slots[i], nil
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
	slots := t.view()
	if i >= uint64(len(slots)) {
		return trapOOBTable
	}
	slots[i] = r
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
// # Serialised against itself, and both copies of the size are inside the section
//
// `growMu` is ADR 0061's mutex with the subject swapped, and 0061's title is why a compare-and-swap
// over `img` is not the answer: the length lives in two places — the image's slice and
// `t.limits.Min`, which `type_of` reads at import-match time — and a CAS reaches only the one in the
// descriptor, relocating the read-compute-write to the copy it cannot see. Two threads growing one
// table would then both read the same old size and one of the two successes would be a lie.
//
// The section is also why `img` is loaded **once** here: two loads would be correct under the lock,
// and one is what makes them obviously so.
//
// **What the lock does not buy, because the racing party takes no lock:** a `table.set` into the old
// array is lost, having landed in the array this function abandons. That is 0058's coherence residual
// with the subject swapped, and it is the half 0065's mechanism deliberately does not repair. What the
// mechanism *does* buy is that the abandoned array stays alive and in bounds for every reader still
// holding the descriptor that names it, so a stale read is a stale value rather than an out-of-bounds
// access.
//
// **This said *"the table's twin of #586 — it needs §4 (#10) to say what is permitted"*, and both halves
// of that are now wrong.** [ADR 0073][0073] answers #586's §4 question by finding its answer set *empty*
// rather than by adding a clause: an agent that stores through an abandoned array and reloads the same
// slot at its next instruction fails to read its own store back in its own program order, which no memory
// model permits. That reading is about the shape and not about memories, so the table half needs no §4
// clause either. The live number was **[#662][662]**, ADR 0073's residual 1, which also records why
// `memory`'s refusal does not simply copy across: a shared memory is *reserved*, so its refusal excludes a
// narrow set of programs, where a table reserves nothing and the same refusal would reach every
// multi-agent growth. This cited #664 for part of #586's slice, which was a duplicate of #662 filed by
// diagnosing the dangling citation without searching the tracker; it is closed as one.
//
// **[Decision 0075][0075] is why the two arms below exist, and the paragraph above is kept as the record
// of what it cost to get here.** The price #662 named — *"a threaded program's table would stop growing
// at its initial capacity"* — is paid down by giving a table the reservation it did not have, bounded by
// the module's own declared max under a measured ceiling, so the two arms below are memory's two arms
// minus the `noMove` one: reslice into the reservation, or relocate only while no sibling agent could
// hold the image.
//
// [0073]: ../../docs/decisions/0073-grow-refuses-to-relocate-when-a-sibling-agent-could-hold-the-old-image-and-the-boundary-accessors-take-the-growth-lock.md
// [0075]: ../../docs/decisions/0075-a-table-reserves-to-its-declared-max-under-a-measured-ceiling-and-refuses-to-relocate-with-a-sibling-agent.md
// [662]: https://github.com/scttfrdmn/burroughs/issues/662
// [669]: https://github.com/scttfrdmn/burroughs/issues/669
func (t *table) grow(delta uint64, r ref, self *thread) int64 {
	t.growMu.Lock()
	defer t.growMu.Unlock()

	cur := t.view()
	old := uint64(len(cur))
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
	// Both arms store a **fresh** descriptor, once — never an assignment through `t.img.Load()`, which
	// would rewrite the three words a reader may be dereferencing and restore the exact hazard 0065
	// removes. `TestAPublishedImageIsImmutableOnceStored` is the control on that.
	switch {
	case newSize <= uint64(cap(cur)):
		// **The reservation arm: the same array at a greater length, so no image is abandoned and
		// there is nothing for a sibling agent to be stranded on.** An older descriptor names the
		// identical pointer with a smaller length, so an agent still holding one writes into slots
		// this array still owns and every such write is visible through the new image.
		//
		// **The fill is not optional and is where memory's twin must not be copied.** `memory`'s
		// reslicing arm publishes `cur[:n]` and says nothing about the new bytes, because `make`
		// zeroed them and zero is what the spec requires of fresh memory. A zeroed `ref` is
		// `{Null: false, Addr: 0, Inst: nil}` — **not** `ref.null`, by `ref`'s deliberate design — so
		// publishing without this loop hands the guest `delta` non-null references that name no
		// function at all where it asked for `r`. That is the same fact `newTable`'s initializer fill
		// is written for, one arm over, and grave #246's in frame locals two over.
		//
		// **What the guest then observes was measured rather than reasoned, and the reasoning was
		// wrong.** This comment read *"a `call_indirect` through one would succeed instead of trapping
		// `uninitialized element`"* until deleting the loop and running it: `ref.is_null` does answer
		// 0, but the call reaches `internal/interp/call.go:funcRefTarget`, which dereferences `r.Inst`
		// unguarded, and the run dies with a nil-pointer panic repanicked out of `invokeIndex`. The
		// *success* reading is inherited from grave #246, which described these bits as "function 0 of
		// the current instance" — true before #170 made resolution go through the reference's own
		// `Inst`, and a wrong answer turned into a crash the day it landed. The unguarded deref is
		// filed as [#669][669]; it is unreachable on main precisely because every `[]ref` allocation
		// site, this one included, fills.
		//
		// No lock beyond `growMu`: every published image has length at most `old`, so no reader can
		// reach the slots being filled, and the atomic `Store` below is what makes the fill visible
		// to anyone who loads the longer image.
		grown := cur[:newSize]
		for i := old; i < newSize; i++ {
			grown[i] = r
		}
		t.img.Store(&tabImage{slots: grown})
	default:
		// **Past the reservation — or on a table that declared no max and therefore has none — the
		// old array is abandoned, so this arm asks first whether abandoning it can strand anybody**
		// (decision 0075, decision 3; ADR 0073's predicate unchanged). A refusal is the spec's `-1`,
		// which `table.grow` already reports for four other reasons, so the *record* of which one
		// happened is the counter rather than the result.
		//
		// **There is no `noMove` arm above this one, and its absence is decided rather than
		// inherited.** A memory carries the mark because an atomic RMW on an abandoned array is
		// invisible to every agent on the new one; nothing addresses a table atomically, so the whole
		// of what a stranded table agent loses is plain writes — exactly the case the predicate
		// answers — and a categorical refusal would exclude strictly more programs for nothing.
		if !t.relocate(newSize, cur, r, self) {
			tableGrowthRefusedWithASiblingAgent.Add(1)
			return -1
		}
	}
	t.limits.Min = newSize
	return int64(old)
}

// relocate blits this table into a fresh array of `newSize` slots, fills the new ones with `r` and
// publishes it, or reports that some agent other than `self` could be holding the image it would abandon.
// `growMu` held; decision 0075's transfer of ADR 0073's decision 1, whose comment on
// `internal/interp/memory.go:memory.relocate` carries the whole argument:
//
//   - **No world: publish.** A table no instance has installed is unreachable by any agent — the
//     `newTable`-and-nothing-else state every unit fixture starts in. `build` attaches before the start
//     function runs, so no *instantiated* table is ever in it.
//   - **One world or several: hold every one of their mutexes across the check, the blit and the
//     publication**, and refuse unless `self` is the sole agent in all of them. The locks span the blit
//     rather than merely preceding it because `enterCall` and `admit` both take `w.mu`: holding it is what
//     makes *"no sibling agent"* true for the duration instead of at an instant, and a caller admitted
//     mid-blit would write into the old array after the copy had read it.
//
// **`relocMu` is memory's mutex and not a twin of it**, which is decision 0075's decision 4: a second
// process-wide ticket would restore the cycle the first exists to prevent — this holding world A's mutex
// and reaching for B's while a memory relocation holds B and reaches for A.
func (t *table) relocate(newSize uint64, cur []ref, r ref, self *thread) bool {
	if len(t.ws) == 0 {
		t.publish(newSize, cur, r)
		return true
	}
	relocMu.Lock()
	defer relocMu.Unlock()
	for _, w := range t.ws {
		w.mu.Lock()
	}
	// Released in reverse, in one deferred pass rather than a `defer` per iteration: `t.ws` cannot change
	// while `growMu` is held, so the set unlocked here is exactly the set locked above.
	defer func() {
		for i := len(t.ws) - 1; i >= 0; i-- {
			t.ws[i].mu.Unlock()
		}
	}()
	for _, w := range t.ws {
		if !w.soleAgentLocked(self) {
			return false
		}
	}
	t.publish(newSize, cur, r)
	return true
}

// publish allocates the new array, copies the old one into it, fills the new slots with `r` and stores the
// image — `grow`'s allocate-and-blit, split out only so that `relocate`'s two arms each name one operation.
func (t *table) publish(newSize uint64, cur []ref, r ref) {
	grown := make([]ref, newSize)
	copy(grown, cur)
	for i := uint64(len(cur)); i < newSize; i++ {
		grown[i] = r
	}
	t.img.Store(&tabImage{slots: grown})
}

// attachWorld records that `w`'s instance holds this table in its index space — decision 0075's
// registration, called from `build` over the fully populated `tables` slice.
//
// Idempotent for one world, for `internal/interp/memory.go:memory.attachWorld`'s reason exactly: a module
// may import the same table twice, filling two slots with one `*table`, and a second entry for the same
// world would make `relocate` take that world's mutex twice and deadlock on the second. The identity test
// is what distinguishes *two slots* from *two instances*.
//
// Under `growMu`, so two concurrent instantiations of importers cannot both miss the other's entry.
func (t *table) attachWorld(w *world) {
	t.growMu.Lock()
	defer t.growMu.Unlock()
	for _, have := range t.ws {
		if have == w {
			return // the same instance naming this table at a second index
		}
	}
	t.ws = append(t.ws, w)
}

// tableGrowthRefusedWithASiblingAgent counts decision 0075's refusal: a table relocation declined because
// some agent other than the grower could be holding the image it would abandon.
//
// **A third counter rather than a wider meaning for either of memory's two**, on
// `growthRefusedPastReservation`'s own argument. `table.grow` reports failure as `-1` and shares that answer
// with four spec refusals, so the record that makes an engine limit distinguishable has to be the
// engine's — and it has to be a *table's*, or a test asserting a table refusal would be satisfiable by a
// memory refusal elsewhere in the process.
//
// **The excluded programs, stated because the limit changes which programs run.** A program with two agents
// on one instance cannot grow a table past its reservation, where a single-agent program can: the grow
// returns `-1`. `tableReserveSlots` is what bounds that set, so the two shapes it covers are a table whose
// declared max exceeds the ceiling and a table that declared **no** max — the second being the larger
// population by far, 113 of the 694 tables the corpus builds. Growth *within* a reservation is unaffected,
// since the array does not move and a stale descriptor names the same slots.
//
// No vector in either corpus reaches this arm — none spawns, and none grows a table with a second agent
// live — so the witnesses are unit ones, in a pair: the refusal *and* a sole-agent grow that still
// relocates and succeeds, because a fix that refused everything would satisfy "no lost write" vacuously.
// `memory.ws` records what the missing half of that pair cost when it was a draft: 30 default-lane passes.
var tableGrowthRefusedWithASiblingAgent atomic.Uint64

// tableReserveSlots caps how much capacity a table reserves at instantiation, in slots.
//
// **The value is derived by a rule that is deliberately not memory's**, and the difference is the element
// type. `sharedReservePages` is the largest reservation whose *worst* allocation clears a 1 ms bar, because
// a memory's reservation is `[]byte` — pointer-free, never scanned, paid once. A table's is `[]ref`: five
// words with three pointers in them, so the whole capacity is scanned on every mark cycle whether or not
// the guest ever grows into it. An allocation ladder cannot see that cost, so the rule here is **the
// smallest ceiling that covers the largest reservable declaration in the corpus** (320 slots, four tables),
// with the ladder used to show where the allocation bar would bite if the ceiling is ever raised.
//
// Best and worst of five, `janus.local` (linux/amd64, i9-9960X), group `measured`, task 19, 0 concurrent
// tasks at submit time, from `BenchmarkTableReservationLadder`. Worst is the column that decides, because a
// fresh arena span is handed out already zeroed and a recycled one is cleared first — an instantiation pays
// whichever it lands on. **Arm A, `newTable` at the rung, against a 1 ms bar:**
//
//	rung        bytes      best        worst        clears 1ms
//	320         12800      360ns       12.346µs     yes
//	1024        40960      590ns       719ns        yes
//	4096        163840     2.498µs     12.728µs     yes
//	16384       655360     8.969µs     290.712µs    yes
//	65536       2621440    138.511µs   570.046µs    yes
//	262144      10485760   38.059µs    4.976204ms   no
//	524288      20971520   65.281µs    11.054218ms  no
//	1048576     41943040   131.939µs   18.06912ms   no
//
// **Arm B, `runtime.GC()` with ten reservations live** — the cost memory's bar cannot see:
//
//	rung        bytes live   best         worst
//	320         128000       372.712µs    618.511µs
//	1024        409600       291.497µs    687.092µs
//	4096        1638400      351.144µs    525.961µs
//	16384       6553600      605.036µs    664.795µs
//	65536       26214400     1.114301ms   1.59612ms
//	262144      104857600    1.876345ms   2.567789ms
//	524288      209715200    3.025685ms   3.602766ms
//	1048576     419430400    5.194508ms   6.231733ms
//
// **1024 is the registered rule's answer and the ladder does not move it**: the smallest rung covering the
// corpus's largest reservable declaration, at 719 ns worst against a 1 ms bar. Neither pre-registered
// rollback fired, and both forecasts were wrong in ways that leave the rule standing — 0075's *Measured*
// section scores them, including the half of forecast (b) that failed.
//
// **The number limits which programs run** — a table whose declared max exceeds it cannot grow past it
// while a sibling agent is live — so it is flagged for review rather than treated as a tuning constant,
// and the excluded programs are stated on `tableGrowthRefusedWithASiblingAgent`. A package-level `var`
// rather than a field for `sharedReservePages`' reason: making it configurable is API-surface design, which
// §0 makes partisan and which is therefore Scott's and chat-Claude's rather than this slice's.
//
// **It is not [#635](https://github.com/scttfrdmn/burroughs/issues/635)'s limit.** That issue is about how
// large a table may *be* — the vacuous `math.MaxInt` guards — and this is how much capacity is reserved
// *ahead* of a growth. Reserving less never admits a larger table, and #635's guards are as vacuous after
// this slice as before it.
var tableReserveSlots uint64 = 1024

// fill writes r into n consecutive slots starting at i, trapping `out of bounds table access`
// when the run does not fit — `table.ml:80-84`'s bound, the same `blit` already checks, stated
// directly rather than filling and catching an overrun mid-loop. `TableFill`'s reference
// (`eval.ml:375-392`) is a recursive store-then-recurse; a loop over the reference's own bound
// check is the same effect without staging n synthetic instructions to re-enter this opcode.
func (t *table) fill(i, n uint64, r ref) error {
	slots := t.view()
	end := i + n
	if end < i || end > uint64(len(slots)) { // `lt_u (add i n) i || gt_u (add i n) j`
		return trapOOBTable
	}
	for k := i; k < end; k++ {
		slots[k] = r
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
	slots := t.view()
	end := offset + uint64(len(rs))
	if end < offset || end > uint64(len(slots)) { // `lt_u (add i n) i || gt_u (add i n) j`
		return trapOOBTable
	}
	copy(slots[offset:], rs)
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
		if err := tab.blit(off, inst.view()); err != nil {
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
