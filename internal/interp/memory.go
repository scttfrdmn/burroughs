package interp

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// pageSize is a wasm page: 64 KiB (`memory.ml:20`, `let page_size = 0x10000L`).
const pageSize = 0x10000

// maxPages32 is the page cap for an i32-addressed memory — `memory.ml:29`'s
// `valid_size`, which is `I64.le_u i 0xffffL` for I32AT and unconditionally true for I64AT.
//
// The bound is on *pages*, not bytes, and it is 0xffff rather than 0x10000: a 65536-page
// memory would be exactly 2^32 bytes, one past the largest i32 address. Reading it off the
// reference rather than deriving it from `math.MaxUint32/pageSize` because those differ by
// exactly this off-by-one and the derivation looks more principled than it is.
const maxPages32 = 0xffff

// trapOOB is the spec's out-of-bounds text (`eval.ml:30`, `Memory.Bounds`).
//
// One value rather than one per site, because the spec has one string: `assert_trap` matches
// it verbatim, and a second spelling would be the engine's testimony disagreeing with itself
// about the same event.
var trapOOB = &Trap{Reason: "out of bounds memory access"}

// memImage is one published state of a memory's contents — [decision 0058][0058]'s descriptor.
//
// **Immutable once published, and that is the whole mechanism.** The *contents* of the array are
// written all the time; the three words that name the array are written exactly once, before the
// descriptor is reachable, and never again. So a reader that holds a `*memImage` holds a pointer and
// a length that were published together, which is what makes it impossible to pair a new length with
// a stale pointer and index past the end of the old array.
//
// A struct rather than an `atomic.Pointer[[]byte]` because the type is where the immutability is
// stated: a reader of `atomic.Pointer[[]byte]` cannot tell from the type whether the header it is
// about to dereference is still being written.
//
// [0058]: ../../docs/decisions/0058-the-memory-image-is-published-through-an-atomic-pointer-because-reachability-is-not-a-spawn-time-property.md
type memImage struct {
	// bytes is the memory's contents. Its length is always a multiple of pageSize, and it is the
	// authority on the current size — the reference reads `size` back out of the array's dimension
	// (`memory.ml:47-50`) rather than keeping a counter, and a second place holding the same fact is
	// how the two drift.
	bytes []byte
}

// memory is one linear memory: a published image of its bytes and the type that bounds them.
//
// **A flat `[]byte` behind one atomic pointer**, which is `memory.ml`'s own shape for the array
// (`create` makes a zeroed Bigarray, `grow` allocates and blits) plus the one indirection
// [decision 0058][0058] pays for. What §1's workload wants — a Go guest that loads once and runs for
// hours — is a memory whose *steady state* is a single contiguous slice, which this is; the sentence
// here used to add *"with no indirection per access"* and that half is what 0058 falsifies, so it is
// removed rather than left for a reader to trust. The indirection's cost is not an estimate: it was
// pre-registered on #575 before the mechanism existed and measured on `internal/interp/membench`.
//
// **Growth moves the backing array for an unmarked memory and never for a marked one**, which is the
// question this comment used to defer to v1 — *"§4's boundary model and shared memories decide
// whether growth may move the backing array at all"* — and #556 is where v1 answered it. Recorded
// here because a deferral left standing after its subject is settled tells the next reader the tree
// is in a state it is not. It said *"unshared"* and *"shared"* until decision 0056 (#572) replaced the
// flag with the `noMove` mark below; the two coincide on every memory this engine can build today, and
// the sentence is written in the terms the code now uses rather than the terms that happen to agree
// with it. The reasons are on `allocate`, on `noMove`, and on `grow`.
//
// **Moving the array is now memory-safe for every memory, marked or not**, which is what 0058 buys
// and what `noMove` no longer has to buy: the mark's remaining job is *coherence*, not safety. See
// `grow`'s reallocating arm.
//
// [0058]: ../../docs/decisions/0058-the-memory-image-is-published-through-an-atomic-pointer-because-reachability-is-not-a-spawn-time-property.md
type memory struct {
	// img is the current published image. Read it once per operation and use the slice it names for
	// the whole of that operation — **two loads in one bounds-check-then-access pair is the defect
	// this field exists to prevent**, since the second load may name a different array than the one
	// the check approved.
	img atomic.Pointer[memImage]

	// limits is the declared type, kept because `grow` needs the max and the address width
	// to decide whether a delta is legal.
	//
	// **`grow`'s write to `Min` is still a plain write**, which 0058 names as a residual rather than
	// fixing: it is one word rather than three, so it cannot produce an out-of-bounds access, and its
	// only cross-thread reader is import matching. Filed with 0058's coherence residual, #586.
	limits binary.Limits

	// noMove records that this memory's backing array must never be replaced — decision 0056's
	// mark, and `grow`'s refusal arm is gated on it rather than on `limits.Shared`.
	//
	// **Set where the reservation happens.** `allocate` is the only site that reserves capacity,
	// so it is the site that sets this, and *reserved ⇒ marked* is an invariant of one function
	// rather than an agreement between two that can drift.
	//
	// **Why a mark and not the flag it replaces.** `limits.Shared` is not a sound answer to "can a
	// second thread reach this array": T-1's `Spawn` (#554) refuses an instance with no shared
	// memory and then runs the entry in the *same* instance, so a spawn-capable instance's
	// **unshared** memories are reachable from two threads too. The flag stays the producer's
	// input at `allocate`; it stops being the consumer's question.
	//
	// **Never read racily, by construction rather than by care.** The only writers are `allocate`,
	// before the memory is reachable at all, and — with #554 — `Spawn`'s walk, which runs while
	// exactly one thread exists. A flag written before any second thread starts is a fact about
	// the past, which is precisely what decision 0056 rejects option (C) for not being.
	noMove bool

	// growMu serialises `grow` against `grow`, which is decision 0061 and is what makes the length
	// change the single atomic read-modify-write the proposal's model calls for
	// (`relaxed.rst:246`).
	//
	// **A second lock rather than `waitMu`, because the two guard unrelated subjects and one of the
	// sections is O(memory size).** The reallocating arm of `grow` is a `make` plus a `copy` of the
	// whole memory; holding the futex queue's lock across that would block every
	// `memory.atomic.wait` and `memory.atomic.notify` on this memory for the duration of a
	// multi-megabyte blit, and those paths have nothing to do with growing.
	//
	// **There is no lock order to get wrong, and that is a property of the call graph rather than a
	// rule anyone is keeping.** `grow` takes this and never `waitMu`; `wait`, `notify` and `detach`
	// take `waitMu` and never this. Neither section nests inside the other, so no ordering exists to
	// be violated — which is worth writing down because a second mutex on one struct is exactly where
	// such a rule usually has to appear, and a later edit that nests them would need to invent one.
	growMu sync.Mutex

	// waitMu guards `waiters`, and holding it across a compare-and-enqueue is what closes the futex
	// miss — decision 0060, and the argument is on `wait`. It adds no constraint on callers of this
	// struct: `img` above already contains an `atomic.Pointer`, so `copylocks` already forbade
	// copying a `memory` by value.
	waitMu sync.Mutex

	// waiters is the wait queue, keyed by **effective address** — one queue per address, not per
	// (address, width), because the proposal wakes the waiters *at an address* and the reference's
	// notify action carries an address and no type. A width-tagged key would decline to wake a
	// `wait32` from a `notify` at the same place, which is a correct program getting a wrong answer.
	//
	// **On `memory` and not on `memImage`, and keyed by an integer and not by a resolved pointer** —
	// both halves of decision 0060's first choice. `memImage` is what a relocating `grow` republishes
	// ([0058]), so a queue there would be abandoned with the array and its waiters orphaned (#586's
	// first half in a second place). And an integer key makes relocation *irrelevant* to a waiter
	// rather than excluded from it: a pointer key would be valid only because `allocate` reserves
	// shared memories, which holds only because `validate` rejects a shared memory with no maximum —
	// a soundness argument owned by another package, and bypassed entirely by a `memory` built as a
	// literal (grave #579's shape).
	//
	// `memory` is also the right identity for free: a shared memory spans instances (0052) and this
	// is the object they share, so two importers wait and notify on one queue without the queue
	// having to know that instances exist. Nil until the first waiter — a memory nobody waits on
	// pays one word, not a map.
	waiters map[uint64][]*waiter
}

// newMemory allocates a memory at its declared minimum.
//
// It reports the reference's two *allocation* failures, and they are separate strings rather
// than one: `alloc` raises SizeOverflow when the minimum exceeds the address width's page cap
// and Type when the limits are inverted (`memory.ml:40-44`). Only the first is reachable here
// — inverted limits are #9's verdict, and this returns the layering debt for them rather than
// a trap, because "min > max" is a claim about the module.
func newMemory(m binary.Memory) (*memory, error) {
	lim := m.Limits
	if !validSize(lim, lim.Min) {
		// `memory size overflow` (eval.ml:31). A trap, not a verdict: the module said a
		// number the format allows and the address width does not.
		return nil, &Trap{Reason: "memory size overflow"}
	}
	if lim.HasMax && lim.Min > lim.Max {
		return nil, fmt.Errorf("%w: memory declares min %d above max %d",
			ErrNotValidated, lim.Min, lim.Max)
	}
	// The multiplication cannot overflow after validSize: an i32 memory is capped at 0xffff
	// pages and an i64 memory's min has already been bounded by what the host can allocate,
	// which is the OutOfMemory the reference also admits (`memory.ml:37`).
	n := lim.Min * pageSize
	if n > math.MaxInt {
		return nil, &Trap{Reason: "out of memory"}
	}
	bs, noMove, err := allocate(lim, n)
	if err != nil {
		return nil, err
	}
	if err := checkBaseAlignment(bs); err != nil {
		return nil, err
	}
	mem := &memory{limits: lim, noMove: noMove}
	// The store is the publication, and it happens before the memory is reachable from anything.
	// `img` is therefore never nil for a memory this constructor returns, which is what lets `view`
	// dereference without a check — and a hand-assembled `&memory{}` would break that, which is
	// grave #163's reason for constructing through the real constructor in tests too.
	mem.img.Store(&memImage{bytes: bs})
	return mem, nil
}

// view is the memory's current contents: one atomic load of the published descriptor.
//
// **Call it once per operation and pass the slice down.** Every caller here binds the result to a
// local and does its bounds check and its access against that one slice, because two calls can return
// two different arrays and a check against the first authorises nothing about the second. That is not
// a style preference — it is the entire content of [decision 0058][0058], and
// `TestEveryMemoryOperationLoadsTheImageAtMostOnce` is what keeps it true as arms are added.
//
// [0058]: ../../docs/decisions/0058-the-memory-image-is-published-through-an-atomic-pointer-because-reachability-is-not-a-spawn-time-property.md
func (m *memory) view() []byte { return m.img.Load().bytes }

// allocate reserves the backing array, and for a **shared** memory it reserves the declared
// maximum as capacity so that `grow` never has to move the array (#556).
//
// The array moving is not a performance question, it is a memory-safety one. A slice header is
// three words and `grow` writes all three; a concurrent reader can observe the new length paired
// with the stale pointer and index past the end of the old array. The spec is explicit that a wasm
// data race is *not* undefined behaviour (`relaxed.rst:248`), so an engine that turns a permitted
// guest race into a Go out-of-bounds read has strengthened the guest's crime into its own. And
// under ADR 0051 the atomics hold a pointer into this array for the duration of an access, which an
// array that can be replaced underneath them makes meaningless — an atomic operation on a
// reallocatable array is not an atomic operation. That is why this is #542's floor rather than an
// arm beside it.
//
// **Reserving is available because the validator already guarantees the max exists.**
// `internal/validate/module.go:checkMemoryType` refuses a shared memory that declares no maximum
// (`ErrSharedMemoryNoMax`), which is the threads proposal's own requirement, so `lim.Max` is always
// known on the branch that needs it. Cited by symbol rather than by line per ADR 0047, so that
// `TestSymbolCitationsResolveToADeclaration` checks it and an insertion above it cannot re-point it.
//
// The branch on `Shared` is stated rather than hidden: an unshared memory has no second observer by
// construction, so §0's performance partisanship says leave its allocate-and-blit alone rather than
// reserve address space no guest can race for.
//
// **"By construction" now has a tripwire, because it is a claim about reachability that this tree is
// about to falsify.** `TestNothingInEngineCodeCreatesASecondObserver` fails on the first `go` statement
// in engine code and carries the instruction to whoever writes it. The gate that looks obvious —
// `limits.Shared` — is not sound: T-1's `Spawn` (#554) refuses an instance with no shared memory and
// then runs the entry in the *same* instance, so a spawn-capable instance's unshared memories are
// reachable from two threads too. **Decided in ADR 0056 (#572), and the `noMove` return value is this
// function's half of it**: the flag below decides whether to reserve, and the mark it hands back is what
// `grow` refuses on, so `limits.Shared` stops being the consumer's question. The other half is `Spawn`'s
// walk, which relocates and marks the unreserved memories while exactly one thread exists (#554) — until
// it lands, the sentence above is still true of every instance this engine can build, because no engine
// code starts a goroutine. What the control cannot see is an embedder calling `Invoke` on one instance
// from two goroutines, which nothing here documents either way.
//
// **The reservation is capped, because reserving `max` outright was pre-registered and measured too
// expensive.** ADR 0051 forecast under 1 ms for the largest declaration the address width allows and
// stated the rollback in advance; the measurement came back at 4.3 ms best and **855 ms worst** for
// `(memory 1 65535 shared)`, three orders over, so the rollback fired. `sharedReservePages` is that
// cap. What is *not* the registered rollback is what happens above it — see `grow`.
func allocate(lim binary.Limits, n uint64) (bs []byte, noMove bool, err error) {
	if !lim.Shared || !lim.HasMax {
		return make([]byte, n), false, nil
	}
	reserve := min(lim.Max, sharedReservePages) * pageSize
	if reserve < n {
		// A declared minimum above the cap is allocated in full and simply cannot grow.
		// `grow` reports that as the spec's -1 rather than pretending otherwise.
		reserve = n
	}
	if reserve > math.MaxInt {
		// The reservation itself is what cannot be served. Reported as the same
		// out-of-memory trap the minimum would have raised, because from the module's
		// side that is what happened.
		return nil, false, &Trap{Reason: "out of memory"}
	}
	// **Decision 0056's condition 1 is this `min` and nothing more, which is worth stating
	// because it looks like it needs code.** Where the declared max is at or below the cap the
	// reservation *is* the max, so arm 1 covers every legal growth and the engine limit can never
	// bite: `grow` refuses above `limits.Max` on the module's own declaration, one check earlier.
	// The refusal therefore reaches only a memory whose max exceeds the cap — or, once #554's walk
	// marks memories that never declared one, a memory with no max at all.
	return make([]byte, n, reserve), true, nil
}

// growthRefusedPastReservation counts decision 0056's condition 2: the named engine limit, kept
// apart from every other reason a grow can fail.
//
// **Why a counter and not a distinct return value.** `memory.grow` does not trap and does not carry
// a reason — it reports failure as `-1` in its result (`memory.ml:60-67`), and inventing a second
// guest-visible answer would be a wrong verdict borrowed from the wrong channel. So the record the
// condition asks for is the engine's, not the guest's: this is incremented on exactly one arm, which
// makes an engine-limit refusal distinguishable from an out-of-memory or over-the-declared-max
// refusal by something an instrument can read. Testing that the two are distinguishable is what
// `TestTheEngineLimitRefusalIsDistinguishableFromEveryOtherRefusal` does with it.
//
// **The excluded programs, stated because the limit changes which programs run.** A memory carrying
// `noMove` whose declared max exceeds `sharedReservePages` cannot grow past that cap. Today that is a
// shared memory declaring more than 128 pages — and nothing in either corpus reaches it, since no
// vector grows a shared memory at all. With #554 it is also every memory in an instance that has
// spawned, including the unshared ones, which is the population that makes this worth naming.
var growthRefusedPastReservation atomic.Uint64

// sharedReservePages caps how much capacity a shared memory reserves at instantiation, in pages.
//
// **The value comes from the measurement that falsified ADR 0051's forecast, not from taste.** Best
// and worst of five `newMemory` calls per size, this host:
//
//	max pages   size     best        worst
//	       64   4 MiB     45.1 µs    474.7 µs
//	      128   8 MiB     12.2 µs    618.6 µs
//	      256  16 MiB     11.8 µs      1.161 ms
//	      512  32 MiB    334.5 µs      2.673 ms
//	     1024  64 MiB    367.9 µs     26.050 ms
//	    65535   4 GiB      4.288 ms  855.438 ms
//
// The spread is the allocator's `needzero`: a fresh arena span is already zero and costs nothing to
// hand out, where a span the allocator has recycled is cleared first — and a 4 GiB memclr is not a
// millisecond. So 128 is the largest size whose *worst* case clears the 1 ms bar the forecast set,
// and taking the worst column rather than the best is the point: the best case is the one a
// benchmark loop and a fresh process both see, and the worst is the one a long-running host hits.
//
// **A package-level var rather than a field, and that is a deliberate non-decision.** Making this
// configurable through the public API is API-surface design, which §0 makes partisan and which is
// therefore Scott's and chat-Claude's rather than this slice's. A var is settable from inside the
// package, which is all any test here needs, and it does not commit the boundary to a shape.
//
// The number limits which programs run — a shared memory declaring a larger max cannot grow past
// this — so it is flagged for review rather than merely recorded. Nothing observable in this tree
// depends on it yet: no vector grows a shared memory, and no threaded guest can run at all until
// T-1 lands. That makes it cheap to move on evidence later and wrong to pick generously now.
var sharedReservePages uint64 = 128

// checkBaseAlignment asserts the premise ADR 0051's atomics rest on: the backing array's base is
// 8-byte aligned in the host's address space.
//
// **It has a second dependent since #557, and that one is a conformance requirement rather than a
// mechanism choice.** ADR 0053 tests tear-freedom eligibility on a slice's own host address
// (`wordAligned`) instead of plumbing the guest effective address to the access site, and the two
// questions coincide *only* because of this assertion: with the base 8-aligned, `&m.view()[ea]` is
// aligned exactly when `ea mod width` is zero, which is the proposal's own condition
// (`runtime.rst:742-746`). So a platform where this failed would not merely lose the atomics — it
// would make the plain-access predicate answer a different question than the one it is documented to
// answer. Both dependents fail the same safe way: one loud refusal at construction, never a silent
// tear.
//
// The threads proposal guarantees an atomic access is naturally aligned *relative to the memory's
// base* (`relaxed.rst:242`), which the interpreter already traps on. `sync/atomic` needs absolute
// alignment, and **Go does document it — in a bug note, which is the whole of why this assertion is
// cheap insurance rather than the thing holding the premise up.** `sync/atomic`'s `BUG(rsc)` block:
// *"The first word in an allocated struct, array, or slice … can be relied upon to be 64-bit
// aligned."* That is this premise, for a slice, at the width wanted. What the Go **specification**
// promises is nothing of the kind — its size-and-alignment section is about `unsafe.Alignof` of
// *types*, not about an allocation's absolute address — so the guarantee exists outside the normative
// document, and one comparison per memory is the cheapest honest response to that gap. Measured
// across 800 allocations from 64 KiB to 16 MiB the base was 8-byte aligned every time, which is not
// the reason it holds: *a measurement is not a guarantee*, and here it is not even the available one.
//
// **The sentence this replaces said Go documents no alignment at all. It was false, and #585 was
// filed on it.** That issue read this function's single caller as a soundness hole: `grow`'s
// reallocating arm publishes a fresh `make([]byte, n)` without calling this, so a grown memory could
// hold an array the constructor would have refused. The narrower truth is that **both allocation
// sites rest on the same documented guarantee**, so the second one is missing a *redundant*
// assertion rather than a necessary one, and no refusal channel for `grow` — and therefore no second
// engine limit — is needed for it. What stands from #585 is a lesson about this comment's own shape
// rather than about the code: it stated coverage in the **constructor's** terms, which reads as a
// guarantee about the type, and one `grep` for this function's callers falsifies it. The coverage is
// now an oracle instead of a sentence — `TestWordAlignedAnswersTheProposalsGuestSpaceCondition` at
// this site and `TestAGrownMemorysPublishedImageIsWordAlignedToo` at the other.
//
// `-race` checks the same premise far more thoroughly, at every access, because it enables
// `checkptr` and every `unsafe.Pointer` conversion in `atomic.go` is instrumented with
// `runtime.checkptrAlignment`. This assertion earns its place by holding in non-race builds too.
//
// **A zero-length memory is legal** — `(memory 0)` appears in `align.wast:3` — so the slice may
// have no first element to take the address of, and there is nothing to check: no access into a
// zero-length memory is in bounds.
func checkBaseAlignment(bs []byte) error {
	if len(bs) == 0 {
		return nil
	}
	if base := uintptr(unsafe.Pointer(&bs[0])); base%8 != 0 {
		return fmt.Errorf("%w: linear memory base %#x is not 8-byte aligned, so the atomics in "+
			"atomic.go cannot use sync/atomic on it (ADR 0051)", ErrUnsupportedOp, base)
	}
	return nil
}

// validSize is `memory.ml:27`'s `valid_size`: an i32-addressed memory is capped at 0xffff
// pages, an i64-addressed one is not capped here at all.
//
// The i64 arm returning true unconditionally is the reference's, not an omission — a memory64's
// size is bounded by the host's allocator rather than by the address type, which is why
// newMemory checks MaxInt separately.
func validSize(lim binary.Limits, pages uint64) bool {
	if lim.Addr64 {
		return true
	}
	return pages <= maxPages32
}

// size is the memory's size in pages, read from the backing slice rather than from a counter
// (`memory.ml:47-50`).
func (m *memory) size() uint64 { return uint64(len(m.view())) / pageSize }

// effectiveAddress is `memory.ml:96`'s `effective_address`: the 64-bit sum of the dynamic index
// and the static offset, trapping when it wraps.
//
// **The index arrives already zero-extended, and that is the whole width story.**
// `value.ml:292` maps an i32 operand through `extend_i32_u` before this is reached, so both
// address widths compute here in 64 bits and the only overflow that matters is the 64-bit one.
// An earlier draft of this engine's design branched on the address type here; the reference
// makes no such distinction, and inventing one would be an accept-direction defect — a wrapped
// i32 address that the reference traps on would instead read some other byte.
func effectiveAddress(idx, offset uint64) (uint64, error) {
	ea := idx + offset
	if ea < idx { // unsigned wrap, `I64.lt_u ea a`
		return 0, trapOOB
	}
	return ea, nil
}

// outOfBounds is `eval.ml:159`'s `oob i n j` — `lt_u (add i n) i || gt_u (add i n) j` — the
// bounds predicate the bulk operations share with each other and with `table.blit`.
//
// Two tests, and the first is not decoration: `i + n` can wrap when both are near 2^64, and
// without the wrap check a wrapped end lands below `j` and admits exactly the access this
// refuses. `read`/`write` express the same bound in the subtraction form (`i > j - n`) because
// they have a `len` in hand and it cannot overflow there; here the reference's form is
// transcribed directly, since these arms' `n` is an *operand* rather than an instruction width.
//
// **A zero-length run at exactly `j` is in bounds and one byte past it is not**, which is the
// whole reason the bulk arms cannot open with an `if n == 0 { return nil }` fast path: the
// reference tests `oob` *before* its `n = 0` exit (`eval.ml:549`, `:567`, `:395`), and
// `bulk.wast` says so in its own words — `:49` "Succeed when writing 0 bytes at the end of the
// region" against `:52` "Writing 0 bytes outside the memory traps". That is the early-return
// grave's shape (#41): a fast path that skips the check it was placed in front of.
func outOfBounds(i, n, j uint64) bool {
	end := i + n
	return end < i || end > j
}

// read returns n bytes at the effective address, or traps.
//
// The bound check is against the *end* of the access and is written so it cannot itself
// overflow: `ea > len - n` rather than `ea + n > len`, since the latter wraps for an ea near
// 2^64 and would admit exactly the accesses this is here to refuse.
func (m *memory) read(idx, offset, n uint64) ([]byte, error) {
	ea, err := effectiveAddress(idx, offset)
	if err != nil {
		return nil, err
	}
	// One load, then both the check and the slice against it (decision 0058).
	bs := m.view()
	if n > uint64(len(bs)) || ea > uint64(len(bs))-n {
		return nil, trapOOB
	}
	return bs[ea : ea+n], nil
}

// write copies bs to the effective address, or traps.
//
// **Nothing is written when the access is out of bounds**, and that is a real property rather
// than a consequence of the loop's shape: `memory.ml:87`'s `store_bytes` counts *downward*
// specifically so the highest address is touched first and a partial store cannot be observed.
// Checking the whole extent up front gets the same guarantee without depending on iteration
// order, which is the property `memory_trap.wast` asserts by reading the memory back after a
// failed store.
func (m *memory) write(idx, offset uint64, bs []byte) error {
	ea, err := effectiveAddress(idx, offset)
	if err != nil {
		return err
	}
	n := uint64(len(bs))
	dst := m.view()
	if n > uint64(len(dst)) || ea > uint64(len(dst))-n {
		return trapOOB
	}
	copy(dst[ea:], bs)
	return nil
}

// writeNum stores width bytes of v at the effective address, or traps.
//
// **The value-taking twin of `write`, and the reason it exists is conformance and nothing else.**
// `write` receives a rendered `[]byte` and `copy`s it, and a `memmove`'s granularity is not a
// guest-visible guarantee — so an aligned `i32.store`, which the threads proposal marks `NOTEARS`,
// could observably decompose. Here an aligned access is one typed host-word store (ADR 0053, #557).
// The byte fallback is the unaligned path, where tearing is permitted.
//
// **It is not here for an allocation, and that is a correction rather than a scruple.** ADR 0053
// forecast that deleting `storeBytes`' 4-byte `make` per store would be the larger of two speedups.
// It was no speedup at all: `storeBytes` inlined into this call site, the `make` was reported *does
// not escape*, and restoring the whole pre-change path measured **zero** heap allocations per store.
// A control written to witness the deletion could not be made to fail and was deleted as stillborn;
// the ADR records the withdrawal, which happened before any benchmark existed. `storeBytes` itself is
// still gone, on the conformance argument alone — a whole-word write needs nothing rendered, the
// fallback writes into the memory directly, and that removed its only caller. Nothing else in the tree
// wanted bytes from a slot (`simd.go` has its own `encode` and `laneBytes`), so it was deleted rather
// than left for `deadcode` to find.
//
// **Truncation is the spec's, and this is where it now lives**: `i32.store8` writes the low byte and
// discards the rest, with no range check, because a store is not a conversion. Both paths truncate —
// `atomicStoreWord` by converting to the width's type, the fallback by shifting — which is a partition a
// falsification has to cross twice, and `TestStoreTruncatesAndIsLittleEndian`'s note says so.
//
// **The out-of-bounds property is `write`'s and is preserved for the same reason**: the whole extent is
// checked before anything is touched, so a trapping store leaves the memory unchanged whichever path
// would have run — which is what `memory_trap.wast` asserts by reading the memory back.
func (m *memory) writeNum(idx, offset, width, v uint64) error {
	ea, err := effectiveAddress(idx, offset)
	if err != nil {
		return err
	}
	bs := m.view()
	if width > uint64(len(bs)) || ea > uint64(len(bs))-width {
		return trapOOB
	}
	dst := bs[ea : ea+width]
	// The aligned arm is atomic (ADR 0054) — see the twin comment in `memAccess`'s load tail for why
	// this is a Go-memory-model requirement rather than a tearing one, and why widths 1 and 2 fall
	// through to `atomicCell`. `cell` re-derives `ea` from `idx`/`offset`, which is the ≈5–8pp the
	// measurement isolated and the reason widths 4 and 8 do not go through it.
	if wordAligned(dst, width) {
		if atomicStoreWord(dst, v) {
			return nil
		}
		c, cerr := m.cell(idx, offset, width)
		if cerr != nil {
			return cerr
		}
		c.store(v)
		return nil
	}
	for i := range dst {
		dst[i] = byte(v >> (8 * uint(i)))
	}
	return nil
}

// grow adds delta pages and returns the previous size, or -1 as the spec's failure value.
//
// **Three failure modes and one return convention.** `memory.grow` does not trap: it reports
// failure as `-1` in the result, so the reference's SizeOverflow/SizeLimit exceptions
// (`memory.ml:60-67`) become that value here rather than errors. Returning an error instead
// would make every failed grow a trap and turn ~53 assert_return vectors into assert_trap
// answers — the wrong verdict, arrived at by borrowing the wrong channel.
//
// **The whole function runs under `growMu`, which is what makes the length change one operation** —
// `relaxed.rst:246` models it as an atomic read-modify-write, and this reads the size, computes a new
// one and publishes, which is three. Decision 0061 (#600) chose the lock over a compare-and-swap on
// `img` for a reason visible only in the last statement of this function: **the size is stored twice.**
// `memImage.bytes`' length is the authority, and `limits.Min` below is a second copy that import
// matching reads back. A CAS over `img` would make the descriptor's copy indivisible and leave the
// other a plain three-step write — the same defect, relocated to the copy the CAS cannot reach. One
// critical section covers both.
//
// The section is also why `img` is loaded **once** here. Two loads were correct before and would be
// correct now; one is what the lock makes *obviously* correct, so the argument that they agree no
// longer has to be made.
//
// **What the lock does not buy, because the racing party takes no lock:** a guest store into the old
// array racing the reallocating arm's `copy` is still lost — it landed in the array this function is
// abandoning. That is decision 0058's coherence residual, it is filed as **#586**, it needs §4 to say
// what is permitted before code can be right about it, and `noMove` below is what excludes it for a
// shared memory. `Spawn` reaches none of this today: no engine code starts a goroutine, so the
// population is an embedder calling `Invoke` on two goroutines.
func (m *memory) grow(delta uint64) int64 {
	m.growMu.Lock()
	defer m.growMu.Unlock()

	cur := m.view()
	old := uint64(len(cur)) / pageSize
	newSize := old + delta
	if newSize < old { // 64-bit wrap: `I64.gt_u old_size new_size`
		return -1
	}
	if !validSize(m.limits, newSize) {
		return -1
	}
	if m.limits.HasMax && newSize > m.limits.Max {
		return -1
	}
	n := newSize * pageSize
	if n > math.MaxInt {
		return -1
	}
	// **A memory with reserved capacity grows by reslicing into it; the pointer never moves.**
	// The condition is the capacity rather than `limits.Shared`, and that is deliberate in both
	// directions: `allocate` reserves capacity only for a shared memory (#556), so this is the
	// shared arm in practice — but an unshared memory whose allocator rounded its size class up
	// would also take it, and reslicing is *correct* there too, since `make` zeroes the whole
	// object and the reservation's tail is therefore already the zero page the spec requires.
	// Testing the property the code actually needs beats testing the flag that usually implies
	// it.
	//
	// The safety argument used to be about what a *concurrent* reader of the slice header can
	// observe — the pointer unchanged and the length only rising, so a torn header pairs a stable
	// pointer with either length, both in bounds, the tail already zero because the reservation came
	// from `make`. **That argument is no longer load-bearing and no longer the only one available.**
	// It was correct for this arm and had no counterpart on the arm below, which is what made *"there
	// is no way to write three words atomically"* the wrong conclusion: decision 0058 does not write
	// three words atomically, it publishes a pointer to three words that are never written again. So
	// both arms now write a fresh descriptor and store it once, and a reader holds whichever
	// descriptor it loaded, entire.
	//
	// The other arm reallocates and copies, matching `grow`'s allocate-and-blit. `append`
	// would also work and would leave the growth factor to the runtime; an explicit make is
	// what keeps the length an exact multiple of pageSize, which `size` reads back as the
	// authority.
	switch {
	case n <= uint64(cap(cur)):
		// The same array at a greater length. The old descriptor stays valid *and in bounds* — it
		// names the identical pointer with a smaller length — so a thread still holding it is
		// reading its own memory, not somebody's freed array.
		m.img.Store(&memImage{bytes: cur[:n]})
	case m.noMove:
		// **Above the reservation a no-move memory refuses to grow, and this is a
		// deviation from ADR 0051's pre-registered rollback that has to be said out loud.**
		// The registration said *"falling back to allocate-and-blit above it, accepting
		// that a shared memory grown past the ceiling needs the header protected some
		// other way."* When this arm was written there was no other way that was both safe
		// and cheap, and worse, the registration did not account for its own decision: ADR
		// 0051 has the atomics holding a raw pointer into this array for the duration of an
		// access, so allocate-and-blit was not merely a torn header — it was a
		// use-after-free, an atomic operating on an array the engine had abandoned while
		// another agent worked on the replacement. *A failed pre-registration narrows, it
		// does not licence*, and shipping the registered fallback because it was registered
		// would have been honouring the letter of the discipline by breaking memory safety.
		//
		// **Half of that reason is now discharged, and the arm survives on the other half.**
		// Decision 0058 found *"some other way"*: the header is published through an
		// atomic pointer, the abandoned array stays alive and in bounds for every thread
		// still holding a descriptor naming it, and the use-after-free is gone for every
		// memory whether marked or not. What is not discharged is **coherence** — an atomic
		// RMW on an abandoned array is invisible to the agents on the new one, so it is not
		// an atomic operation in any sense the model recognises, and *that* is what a shared
		// memory cannot be allowed to reach. So this arm's subject changed while its code
		// did not: it refuses in order to keep an agent from being left behind, not in order
		// to keep an array from being freed. Recorded rather than rewritten silently,
		// because a comment stating a safety argument its decision has retired is the
		// defect-stated-as-the-rule shape: review would confirm the arm for a reason that no
		// longer holds.
		//
		// `-1` is the conforming alternative, and it is conforming rather than convenient:
		// `memory.grow` does not trap, it reports failure in its result, and the reference
		// itself fails a grow for reasons of its own (`memory.ml:60-67`'s SizeOverflow and
		// SizeLimit). An engine limit reported through the channel the spec provides for
		// engine limits is a true answer. A guest that needs more growth room than
		// `sharedReservePages` gets a legal refusal instead of a wrong success.
		//
		// Nothing in either corpus reaches this arm — no vector grows a shared memory at
		// all — so the board cannot witness it, and
		// `TestSharedMemoryGrowthKeepsItsBackingArray` is what stands in for one.
		//
		// **The condition is `noMove` and not `limits.Shared`, which is decision 0056.**
		// `limits.Shared` is not a sound answer to "may this array be replaced": T-1's
		// `Spawn` (#554) runs a second thread in the *same* instance, so a spawn-capable
		// instance's **unshared** memories are reachable from two threads too, and a
		// `Shared`-gated refusal drops them onto the arm below that moves the pointer. The
		// mark says what this arm needs to know; the flag says what `allocate` needed to
		// know. Behaviour today is identical — shared ⇒ reserved ⇒ marked — which is why the
		// unit control above is the only witness there can be.
		//
		// **And it is a named engine limit rather than an anonymous `-1`**, which is the
		// second of the two conditions decision 0056's ruling carries. The `-1` is shared
		// with three other failure modes a few lines up, so the *record* is where the naming
		// has to happen: `growthRefusedPastReservation` is incremented on this arm and on no
		// other, which is what makes an engine-limit refusal distinguishable from an
		// over-the-declared-max or address-width one by something other than reading this
		// code. The excluded programs are stated on the counter.
		growthRefusedPastReservation.Add(1)
		return -1
	default:
		// **Relocation is memory-safe here and is not coherent, and decision 0058 says so rather
		// than leaving the difference to be discovered.** The abandoned array is not freed while any
		// thread holds a descriptor naming it, and every such descriptor is internally consistent, so
		// there is no out-of-bounds read and no use-after-free — which is the whole of what the
		// `noMove` arm above had to buy before 0058. What relocation still costs is *coherence*: a
		// thread left on the abandoned array loses the updates it makes there, and an atomic RMW it
		// performs there is invisible to every agent on the new array. The class of the defect
		// changed from memory unsafety to a lost update in the value domain, which the spec permits
		// for plain accesses on a memory that is not shared and does not describe for atomics on one.
		// That is why `noMove` stays: a reserved memory never reaches this arm, so no agent is ever
		// left behind on a shared memory. The residual is filed as **#586**, a fifth precondition on
		// unparking `Spawn` rather than a defect in this arm.
		grown := make([]byte, n)
		copy(grown, cur)
		m.img.Store(&memImage{bytes: grown})
	}
	// **The declared type grows with the memory, and it is mutable for exactly this
	// reason.** `memory.ml:64`'s `grow` sets `mem.ty <- MemoryT (at, lim')` with `lim'.min`
	// the new size — `type_of` (called at import-match time, `instance.ml:76`) reads that
	// field back, so a memory this instance re-exports after growing must satisfy an
	// importer against its *current* size, not the size it had when this instance was
	// built. `imports4.wast:22-37` pins exactly this, in its own comment: "imported memory
	// limits should match, because external memory size is 2 now."
	m.limits.Min = newSize
	return int64(old)
}

// runData performs one data segment's instantiation-time effect — `run_data`
// (`eval.ml:1278-1291`), the trapping half of 0015.
//
// **Two modes, not three, and one of them drops.** A data segment is Passive (no effect) or
// Active (copy, then `data.drop`); the reference's Declarative arm is `assert false` for data,
// which is why `DataSegment.Passive` is a bool where `ElemMode` is an enum. So this is
// `runElem`'s shape minus the declarative case, and the drop is load-bearing for the same
// reason — `bulk.wast:153-173` is the memory twin of the `init_passive`/`drop_passive` pattern,
// with `:172` asserting `out of bounds memory access` for a one-byte init out of a dropped
// segment.
//
// **The trap is the point.** `data1.wast` is 14 vectors of `assert_trap` wrapping a bare module,
// every one expecting `out of bounds memory access`, none of them invoking anything: a segment
// whose extent exceeds its memory is a module that *traps while coming to life*, not a module
// that is invalid. That distinction is the suite's, which is what made it the design's (0015).
//
// A segment naming a memory the module does not have is #9's verdict and is reported as the
// layering debt — the module is wrong, not the run. Note that `data1.wast:3` deliberately
// exercises the *other* case: `(data (memory 1) …)` against three declared memories is a
// legal index whose target is too small, which traps.
//
// **Resolution goes through memoryFor rather than indexing `in.mems` directly**, which is one
// concept getting one trigger. The first draft wrote its own bound check here — `MemIndex >=
// len(in.mems)` — and got half of memoryFor's job: it caught the out-of-range index and not the
// *reserved-but-empty* slot, so as soon as import slots became reachable a data segment aimed at
// an imported memory dereferenced nil and panicked. Two places knowing "how to turn a memory
// index into a memory" is the shape that produced graves #78, #105 and #106; there is now one.
func (in *Instance) runData(idx int, seg *binary.DataSegment) error {
	if seg.Passive {
		return nil
	}
	inst, err := in.dataFor("data segment", uint64(idx))
	if err != nil {
		return err
	}
	mem, err := in.memoryFor("data segment", uint64(seg.MemIndex))
	if err != nil {
		return err
	}
	off, err := in.constAddr(seg.Offset, "a data segment's offset")
	if err != nil {
		return err
	}
	// Offset 0 with an empty segment is in bounds for a zero-length memory, which `write`
	// gets right for free: `n == 0` makes the extent check `ea > len`, and ea is 0.
	//
	// A trapping copy does not drop, for `runElem`'s reason and with the same ordering.
	if err := mem.write(off, 0, inst.bytes); err != nil {
		return err
	}
	inst.drop()
	return nil
}

// The offset evaluator that used to live here is `constAddr` in constexpr.go, one of the four call
// sites of #241's single constant-expression evaluator. It moved rather than stayed because it was
// never a memory concern: it was the *general* const-expr path with a data segment's name on its
// error message, which is exactly the misattribution grave #240 filed.

// memoryFor resolves a memory index to a memory. It is the *only* place that does, which is what
// keeps its two failure modes from being half-remembered elsewhere (see initData).
//
// `what` names the thing holding the index — "instruction", "data segment" — because the error is
// read by someone looking for it in their module, and "instruction names memory 3 of 2" sends
// them to the wrong line when the index was in a `(data (memory 3) …)`.
//
// Reported as the layering debt rather than as a trap: an index past the end of the memory
// index space is a module the validator rejects, and this package's rule is that it never
// invents a verdict about a module.
func (in *Instance) memoryFor(what string, idx uint64) (*memory, error) {
	if idx >= uint64(len(in.mems)) {
		return nil, fmt.Errorf("%w: %s names memory %d of %d",
			ErrNotValidated, what, idx, len(in.mems))
	}
	if in.mems[idx] == nil {
		// A reserved slot with nothing in it, and the two reasons are reported apart because
		// they are different facts about the engine. An index below the import offset is an
		// **imported** memory *nothing supplied* — nothing went wrong with the module. Above
		// it, a declared memory whose allocation failed for a verdict-shaped reason, and the
		// reason is quoted rather than paraphrased: without it this reads "memory %d exists but
		// is nil", which tells the reader nothing about their module.
		//
		// **This arm is now the unlinked case rather than the only case, and no line of the
		// *logic* changed to become that.** `InstantiateLinked` fills the reserved slot, so a
		// supplied memory reaches the check below and is simply returned — supplying an import is
		// *filling*, never restructuring an index space, which is what the reserved-not-omitted
		// convention bought (22 vectors, see Instance.mems). The sentence that used to say v0
		// "cannot supply" this was true when written and is the prose a landing falsifies.
		//
		// **The message did change, and it had to**: it said `linking is not implemented`, which
		// this engine falsifies the moment it links, and an error naming a component's absence
		// while the component runs is the fabricated-evidence grave (#36) — the right verdict
		// quoting a fact the engine does not hold. The bucket-preservation argument that licensed
		// keeping it verbatim was discharged when the drain was measured: **624 → 13** under the
		// old string, so the key it protected has served its one purpose, and the 13 that remain
		// are unsupplied imports rather than an engine without a linker. Still ErrUnsupported —
		// "this phase does not run a module whose import went unsupplied" is true and is §3's.
		if idx < uint64(in.mod.ImportedMems()) {
			return nil, fmt.Errorf("%w: memory %d is an import nothing supplied (contract §3)",
				ErrUnsupported, idx)
		}
		return nil, fmt.Errorf("%w: memory %d was declared but not allocated: %w",
			ErrNotValidated, idx, in.deferred)
	}
	return in.mems[idx], nil
}

// memAccess executes one load or store from the 0x28-0x3e family.
//
// **Operand order is the stack's, and it is the half of this that a plausible-looking arm gets
// backwards.** A store pops the *value* first and the *address* second (`eval.ml:462`,
// `Store …, Num n :: Num i :: vs'`), because the address was pushed first. Reversing them
// produces a store to the value's address with the address's value — which for the many vectors
// whose address and value are both small integers writes plausible bytes to the wrong place.
func (in *Instance) memAccess(ins binary.Instr, st *stack) error {
	m, ok := memops[ins.Op]
	if !ok {
		// Not reachable from run's case list, which is derived from the same table's
		// domain; stated rather than assumed, since a divergence here would silently make
		// a load a no-op.
		return unsupported(ins)
	}
	// `resolveErr`, not `err`: the two `needNum` checks below bind their own `err` in inner
	// scopes, and reusing the name here would shadow this one — flagged by `govet`, and worth
	// renaming rather than suppressing because the two errors mean different things (no memory
	// versus no operands) and a single name invites returning whichever is in scope.
	// **Through binary.Memarg, not `ins.Imm1` bare.** This line read the whole second word
	// as a memory index, which was correct only for as long as nothing packed above bit 32
	// on these 23 rows — the SIMD lane paths next door already masked, because their lane
	// index had taught them to. #306 packs an alignment exponent at bits 40-45, so the bare
	// read would resolve `i32.load align=4` to memory index `2<<40`: not a subtle wrong
	// answer but every aligned load in every module failing to find its memory. The accessor
	// is the fix for the class rather than for this line (module.go's memarg comment).
	offset, memIdx, _ := binary.Memarg(ins.Imm0, ins.Imm1)
	mem, resolveErr := in.memoryFor("instruction", uint64(memIdx))
	if resolveErr != nil {
		return resolveErr
	}
	if isStore(ins.Op) {
		if err := st.needNum(2); err != nil {
			return err
		}
		v := st.popNum()   // the value, pushed second
		idx := st.popNum() // the address, pushed first
		// `writeNum`, not `write(…, storeBytes(…))`: an aligned store must not tear, and a
		// rendered byte slice reaching `copy` is exactly the decomposition the proposal forbids
		// (ADR 0053).
		return mem.writeNum(mem.addr(idx), offset, m.width, v)
	}
	if err := st.needNum(1); err != nil {
		return err
	}
	idx := st.popNum()
	bs, err := mem.read(mem.addr(idx), offset, m.width)
	if err != nil {
		return err
	}
	// **The tear-freedom branch lives here rather than inside `loadValue`, and its placement is a
	// measurement.** An aligned integer access up to 32 bits must not tear (`runtime.rst:742-746`),
	// which a byte loop violates by construction — but hosting the branch in `loadValue` made that
	// function too complex to inline (cost 65 → 165 against an 80-point budget) and cost 6.24% on
	// every *unaligned* load, the one class the change was supposed to leave alone. Both arms here are
	// inlinable on their own, so the branch is free and the cost lands nowhere. ADR 0053 records the
	// figure, and this is what its registered rollback was for — the mechanism it named (a precomputed
	// width flag) would not have touched the cause.
	//
	// **The aligned arm is atomic, not merely untorn (ADR 0054).** 0053 chose one typed word access
	// here so an aligned access could not decompose; 0054 makes that same access sequentially
	// consistent, because a plain host read racing a host atomic write is a Go data race whether or not
	// it tears — and a scoped gate that would confine the racy region is *unavailable* rather than
	// unwritten, `Spawn` being ambient on any instance. `atomicLoadWord` declines widths 1 and 2, which
	// have no single atomic instruction, and those route through `atomicCell`'s CAS loop at the cost of
	// re-resolving the effective address.
	if wordAligned(bs, m.width) {
		if v, ok := atomicLoadWord(bs); ok {
			st.pushNum(extendSlot(v, m))
			return nil
		}
		c, cerr := mem.cell(mem.addr(idx), offset, m.width)
		if cerr != nil {
			return cerr
		}
		st.pushNum(extendSlot(c.load(), m))
	} else {
		st.pushNum(loadValue(bs, m))
	}
	return nil
}

// addr narrows a popped slot to the memory's address width.
//
// **Zero-extension, not sign-extension**, and the reference is explicit: `value.ml:292` maps an
// i32 operand through `extend_i32_u`. So `i32.load` at address `-1` is address `0xFFFFFFFF` —
// far out of bounds and trapping — rather than address `0xFFFFFFFFFFFFFFFF` or a wrap to 0. An
// i64 memory's slot is already the address.
//
// The mask is applied here rather than at the pop site because the *memory* decides the width,
// not the instruction: the same `i32.load` opcode is 32-bit addressed against a memory32 and
// (with the memory64 gate on) 64-bit addressed against a memory64.
func (m *memory) addr(slot uint64) uint64 {
	if m.limits.Addr64 {
		return slot
	}
	return uint64(uint32(slot))
}

// An `errIsTrap` predicate stood here and was **never called**, `asTrap` (interp.go) having been
// written for the same job one file over and actually used. `deadcode` found it, which is the
// classification question working as decision 0005 intends: two helpers for one concept is the
// two-places-know-one-fact shape, and the one to keep is the one the instantiation boundary calls.
// Recorded rather than silently deleted because the *reason* it existed — 0015's channel split must
// be decided by type, not by convention — is a live requirement that `asTrap` now carries alone.
