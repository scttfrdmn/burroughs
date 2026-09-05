// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// HostFunc is an embedder-supplied function a module can import — contract §5's whole subject, and
// [ADR 0069][0069]'s option A as ruled.
//
// **The arguments arrive boxed and the results go back boxed, and that cost is the choice.** The
// alternative on the table was a raw-stack form where the host reads and writes the operand stack in
// place, which is faster and hands the embedder a soundness burden: an embedder who miscounts corrupts
// the interpreter's stack. Scott's ruling on the #647 review took A on reversibility rather than on
// speed — *"a soundness burden pushed onto embedders can't be taken back once anyone depends on it"* —
// so the per-call `[]Value` allocation and the boxing are accepted, and the fast path stays namable as a
// later opt-in rather than a default.
//
// **A returned error is a trap**, and it is the channel §5 H-3 needs: a call interrupted by shutdown
// must not return success, so an embedder that respects `Caller.Context` returns `ctx.Err()` and the
// engine renders it as a trap the guest cannot catch. There is deliberately no second out-parameter for
// "cancelled" — one channel, so a cancelled call cannot be spelled as a successful one.
//
// [0069]: ../../docs/decisions/0069-a-host-function-is-a-caller-and-a-value-slice-a-host-call-marks-its-thread-blocked-and-shutdown-is-its-own-terminal-method.md
type HostFunc func(c *Caller, args []Value) ([]Value, error)

// Caller is what a host function is told about its caller, and **what it is deliberately not told.**
//
// # H-2 is enforced by absence
//
// §5 H-2: *"Host calls MUST NOT re-enter the guest on the caller's stack (no surprise reentrancy)."*
// There is no `Invoke` here, and no accessor that reaches an `*Instance`, so the method that would
// violate H-2 does not exist. That is the enforcement — Scott's fourth sub-choice, verbatim: *"H-2
// enforced by `Caller` having no entry point on it — no control needed, because the method that would
// violate it will not exist."* A control asserting "no reentrancy method" would be a control over a
// method's absence, which the compiler already asserts for every embedder in the world.
//
// # Guest memory is reachable, and it is **copied rather than viewed**
//
// This paragraph replaces one that said there was no accessor and named the gap as escalated surface.
// Scott ruled it on the #651 review: *"`Caller` gets guest-memory access — but as copying accessors,
// not a view. `Caller.Read(offset, n) ([]byte, error)` and `Caller.Write(offset, buf) error`."*
//
// **The reason is a soundness burden and not a taste in APIs**, and it is his: *"A retained slice would
// alias a memory that can grow and relocate — #575 and #622's exact subject — which is the soundness
// burden option B was rejected for. Exposing a view reopens the story A was chosen to close."* The
// mechanism is in `memory.grow`: the reserved-capacity arm reslices and the pointer holds, but the arm
// below it **reallocates and blits**, and a slice handed out before that runs names the abandoned array
// afterwards. A read through it is stale, and a *write* through it is worse than stale — it lands in an
// image no guest will ever load again, so the store is silently lost with nothing anywhere reporting it.
// `noMove` exists because ADR 0051's atomics hold a raw pointer for the duration of one access; a slice
// an embedder may hold for the duration of its own choosing is that hazard with no bound on it.
//
// **The cost is named rather than discovered, and it is a cost A has already agreed to pay.** Copying is
// an allocation per access — *"the same trade A already makes with boxed `[]Value`, so it's consistent
// rather than a new tax"* — and the escape hatch is additive: *"A view stays addable later as an additive
// fast path, exactly like C's identity and B's stack form."* So the shape of the eventual widening is
// known and none of it is foreclosed here.
//
// # Which memory, and why that is not the question the world was
//
// **Memory 0 of the instance that *declared* the import**, which is `callHost`'s receiver — deliberately
// *not* the thread-derived choice the world question took. The two look alike and are different
// questions. A world answers *who may cancel this thread*, which is a fact about the running agent, and
// taking it from the receiver was the defect `TestAHostCallIsCountedAndWaitedForByTheCallersWorld` now
// pins. "Memory 0" answers *whose index space is that*, which is a fact about the module that wrote
// `(import "h" "f" …)` — the embedder's function was written against that module's memory, so in the
// two-instance shape where `top` reaches a host import `mid` declared, the bytes are `mid`'s. Stated at
// length because the neighbouring question has the opposite answer, and a later reader who transfers one
// to the other has a defect in whichever direction they carry it.
//
// A module with no memory at all is `ErrNoMemory` rather than a panic or a zero-length success.
//
// # A `Caller` outliving its call is memory-safe, and that is not the same as being meaningful
//
// Nothing here refuses a retained `Caller`, and the accessors are safe on one anyway: they copy, and they
// load the image through `m.img` atomically, so a late read yields whatever image was current and never a
// freed array. What a late read does *not* have is a meaning — the call has returned, and after `Close`
// the bytes are a corpse the embedder is measuring. Invalidation was considered and not taken: nil'ing
// the field after the call is one store, but an embedder reading from another goroutine races that store,
// and buying a diagnostic with a data race is the wrong trade. The ruling asked for copying accessors and
// this is a copying accessor; the lifetime rule stays addable beside the view.
type Caller struct {
	// ctx is the *thread's* context, created in `world.addLocked` and cancelled by `Instance.Close`.
	// Per thread rather than per call, which is Scott's second sub-choice and the reason a host call
	// allocates no context: *"created once per thread so it is not a per-call allocation."*
	//
	// **`addLocked` and not `newThread`, which [ADR 0069][0069]'s third choice originally named.**
	// `newThread` is `Spawn`'s alone, and the *ordinary* host call — an embedder `Invoke`s, the guest
	// calls a host import — runs on `in.host`, which link.go builds by literal and `register`s. A
	// context created in `newThread` would therefore be nil on exactly the thread every host call in
	// the tree today runs on, and `Close` would cancel nothing. `addLocked` is where `t.w` is
	// established and is documented as *"the one place membership and `t.w` are established, so the
	// invariant … cannot be half-kept by one of two callers"* — which is the same invariant a
	// cancellable thread needs, so it is the same function.
	//
	// [0069]: ../../docs/decisions/0069-a-host-function-is-a-caller-and-a-value-slice-a-host-call-marks-its-thread-blocked-and-shutdown-is-its-own-terminal-method.md
	ctx context.Context

	// tid identifies the calling thread. A `ThreadID` rather than a `*thread`, so nothing an
	// embedder holds can reach the world, the mutex, or the safepoint state.
	tid ThreadID

	// mem is memory 0 of the declaring instance, or nil when there is no memory to reach. A `*memory`
	// and not an `*Instance`, which is the same containment `tid` buys one field up: the accessors
	// need bytes, and handing over the instance would hand over `Invoke` and void H-2 by widening.
	mem *memory

	// memErr is why `mem` is nil, when the reason is one `memoryFor` can state better than
	// `ErrNoMemory` can. **It exists to stop a flattening, not to add a field.** Three different
	// facts produce no reachable memory 0: the module declares and imports none, an *imported* memory
	// went unsupplied (§3, `ErrUnsupported`), or a declared one failed to allocate
	// (`ErrNotValidated`). Only the first is what `ErrNoMemory`'s words say, and reporting the other
	// two as "defines and imports no memory" would be an error message asserting something the engine
	// knows to be false — `memoryFor`'s own comment is about that exact mistake, and grave #36 is
	// where this project paid for it.
	//
	// Resolved once in `callHost` rather than on each access, because a `Caller` holds no `*Instance`
	// to resolve through later, which is H-2 doing its job at a small cost in a field.
	memErr error
}

// Context is the thread's cancellation channel — §5 H-3's *"interruptible by engine shutdown"* half.
//
// It is `Done()` after `Instance.Close`, and a host function that intends to block MUST select on it:
// H-3 is a MUST on the engine, and the engine's half is cancelling and then waiting. An embedder that
// ignores it makes `Close` wait forever, which is a hang in the embedder's function and not in the
// engine — stated because the alternative reading is that `Close` should abandon the call, and
// abandoning a Go function is not a thing Go can do.
func (c *Caller) Context() context.Context { return c.ctx }

// Thread identifies the calling guest thread, so a host function serving N agents can tell them apart —
// which every one of §§3–5's N-agent litmus rows needs, and which is the whole reason this is here
// rather than a later widening.
func (c *Caller) Thread() ThreadID { return c.tid }

// Read copies n bytes of guest memory from offset, or reports why it could not.
//
// **The copy is the whole ruling** — see `Caller`'s own comment for why a view is refused. `memory.read`
// hands back a sub-slice of the live image, so returning its result would be exactly the aliasing
// forbidden; the `copy` here is what makes the returned bytes the embedder's own.
//
// **Bounds before allocation, and the order is load-bearing.** `read` checks the extent against the image
// it loaded and only then is `n` known to be no larger than the memory, so `Read(0, 1<<40)` fails with a
// trap rather than asking the allocator for a terabyte first. Writing the `make` above the check would be
// an out-of-memory kill on a path whose whole job is to refuse the access.
//
// **One image, entire.** `read` takes a single `m.view()` load and both checks and slices against it
// (decision 0058), so a concurrent `grow` cannot make one `Read` straddle two images. The copy is plain
// and a concurrent guest write to the same bytes may therefore be observed torn — permitted, since the
// threads proposal permits the guest's own racing accesses to tear, and classified as such in
// `guestMemoryRegimes`. An out-of-bounds `Read` returns the identical `*Trap` a guest `i32.load` would
// get, so an embedder's overrun and a guest's overrun are one error value rather than two dialects.
func (c *Caller) Read(offset, n uint64) ([]byte, error) {
	mem, err := c.guestMemory()
	if err != nil {
		return nil, err
	}
	// §4 B-MM-1, at the third site that runs no guest code: this reads guest storage directly, and a
	// stale read is what the acquire edge forbids — `Global`'s reason, one type over.
	enterGuest()
	defer leaveGuest()

	// `offset` arrives as `read`'s *first* parameter and the second is zero. The pair is a wasm
	// address operand plus a static `offset` immediate, and a host access has no immediate; passing
	// the whole address as the dynamic half is what keeps `effectiveAddress`'s wrap check meaningful.
	bs, err := mem.read(offset, 0, n)
	if err != nil {
		return nil, err
	}
	out := make([]byte, n)
	copy(out, bs)
	return out, nil
}

// guestMemory answers with memory 0 or with the best available reason there is none — `memErr`'s
// distinction, at the one place both accessors reach it.
func (c *Caller) guestMemory() (*memory, error) {
	if c.memErr != nil {
		return nil, c.memErr
	}
	if c.mem == nil {
		return nil, ErrNoMemory
	}
	return c.mem, nil
}

// Write copies buf into guest memory at offset, or reports why it could not.
//
// **The copy runs the other way and the engine retains nothing** — `memory.write` copies out of `buf`, so
// an embedder may reuse or mutate the slice the instant this returns. That is the same no-aliasing
// property `Read` buys in the other direction, and it holds here for free rather than by a deliberate
// copy, which is why this method is the short one.
//
// **Nothing is written when the access is out of bounds**, inherited from `write`: the whole extent is
// checked before a byte moves, so a refused `Write` leaves the memory as it was. A partial write followed
// by an error would be the worst of the available behaviours, and it is the one `memory_trap.wast` reads
// the memory back to exclude for the guest's stores.
func (c *Caller) Write(offset uint64, buf []byte) error {
	mem, err := c.guestMemory()
	if err != nil {
		return err
	}
	enterGuest()
	defer leaveGuest()
	return mem.write(offset, 0, buf)
}

// hostMemory resolves the memory a `Caller`'s accessors reach, keeping `memoryFor`'s three distinct
// reasons for having none instead of flattening them (see `Caller.memErr`).
//
// **`(nil, nil)` is a real answer here and means "this module has no memory".** `memoryFor` would call
// that `ErrNotValidated: … names memory 0 of 0`, which is false of a perfectly valid memoryless module —
// so the empty index space is answered before asking it, and the accessor supplies `ErrNoMemory` as the
// word for it. A host function in a memoryless module must still *run*, so this cannot be an error on the
// call path: it is only an error when somebody asks for bytes.
func (in *Instance) hostMemory() (*memory, error) {
	if len(in.mems) == 0 {
		return nil, nil
	}
	return in.memoryFor("a host function's guest-memory access", 0)
}

// ErrNoMemory reports a guest-memory access from a host function whose declaring module defines and
// imports no memory. Distinct from a trap on purpose: an out-of-bounds access is the guest's error
// vocabulary and is reported in it, while asking a module with no memory for bytes is a mismatch between
// the embedder's function and the module it was linked into — nothing the guest did.
var ErrNoMemory = errors.New("the instance that declared this host function has no memory 0")

// hostFunc is a host function plus the type it presents to the linker. Unexported, and reached only
// through `HostExtern`, so an embedder cannot build one with a type that does not match its body.
type hostFunc struct {
	ft binary.FuncType
	fn HostFunc
}

// HostExtern makes an `Extern` that satisfies a function import with an embedder's Go function.
//
// **The type is given rather than inferred, because Go's type system cannot spell a wasm functype.**
// `func(*Caller, []Value) ([]Value, error)` is the same Go type for every wasm signature, so the linker
// has nothing to match an import against unless the embedder says what the function's wasm type is. A
// mismatch between `ft` and what `fn` actually reads and returns is caught at the call — `callHost`
// checks the results against `ft` — rather than being trusted.
//
// **A `binary.FuncType` from outside a module may not name type indices**, and that is a named engine
// limit rather than an oversight: a type-index-bearing ref type such as `(ref $t)` is an index into some
// module's type section, a host function has no type section, and `importTypeMismatch`'s func arm
// compares through `internal/validate`'s relation over *two* modules' type spaces. `hostTypeIsLinkable`
// refuses those at link time with their own message. Widening it is the component model's business
// (contract §6), and refusing is what keeps this slice from pre-deciding it.
func HostExtern(ft binary.FuncType, fn HostFunc) Extern {
	return Extern{Kind: binary.ExternFunc, host: &hostFunc{ft: ft, fn: fn}}
}

// ErrHostTrap wraps whatever a host function returned as an error. The guest sees a trap; the
// embedder's own error is joined with `%w` so `errors.Is(err, context.Canceled)` still answers on a
// cancelled call, which is what `h3-shutdown-interrupts-a-parked-agent` reads.
var ErrHostTrap = errors.New("host function trapped")

// ErrHostSignature reports a host function that returned something other than what its declared
// `binary.FuncType` promises. An engine-side refusal rather than a trap: nothing is wrong with the
// module, and blaming the guest for an embedder's arity error is the mistake `tableFor`'s rule names.
var ErrHostSignature = errors.New("host function result does not match its declared type")

// ErrClosed reports an operation on an instance `Close` has terminated. Terminal by construction —
// there is no `Resume` counterpart, because §5 H-3's shutdown is a teardown and §3 SP-4's stop is a
// pause. That distinction is Scott's own correction on the #646 review (recorded at
// [#602](https://github.com/scttfrdmn/burroughs/issues/602)): *"A pause must not disturb a blocked host
// call; a teardown must interrupt it."*
var ErrClosed = errors.New("instance is closed")

// callHost runs a host function with the arguments already on the shared operand stack, and leaves its
// results there — `invoke`'s contract with none of `invoke`'s frame.
//
// # The three things that happen around the embedder's function, and why each is not optional
//
// **The boundary crossings.** `leaveGuest` out and `enterGuest` back, which is §4 B-MM-1's release edge
// and its acquire edge for *"host-call return"* — the site B-MM-1 names **first** and the engine has not
// had (grave #645). Unannotated, so sequentially consistent under B-MM-4's convention in `boundary.go`.
//
// **The blocked mark.** `enterBlocked`/`leaveBlocked`, the same pair `memory.atomic.wait` uses, and the
// line that makes three litmus rows true rather than hoped for. ADR 0067's predicate is
// `blocked == callers`: the guest frame that made this call is still counted as a caller, so **without
// the mark a thread parked in an embedder's `select` reads to `Stop` as running guest code** and the stop
// waits out its whole deadline. With it, SP-2 counts the parked thread as arrived without waking it,
// which is SP-4's requirement in the same clause. `enterBlocked` also parks first when a stop is already
// in flight, so a host call cannot *begin* during a stop.
//
// **The closed check.** A `Close`d instance refuses to begin a host call, because `Close` returns when
// every in-flight call has returned and a call admitted after that would be outside the wait it just
// completed — `admit`'s indivisibility argument, one subject over.
//
// # An embedder panic tears three of those four, and the guard cannot see it — #650
//
// Only `endHostCall` is `defer`red. The two crossings are straight-line because `enterGuest` must be
// established *before* `pushHostResults` touches guest state and a `defer` would run it after; the blocked
// mark is straight-line beside it, and `enterCall`/`leaveCall` one layer out are straight-line for a
// **measured** reason recorded at their own site (a second `defer` there took `defer leaveGuest()` off the
// open-coded path — ADR 0067). So a panic out of `h.fn` skips `leaveBlocked`, `enterGuest`, and
// `leaveCall`.
//
// **And `blocked` and `callers` leak *together*, which is why nothing fires.** The arrival predicate's
// guard panics on `blocked > callers`; here both are one too high, so the predicate reads
// `blocked == callers` — SP-2's *arrived* — on a thread that is executing nothing. An embedder that
// recovers the panic above its own `Invoke` then has a live instance whose next `Stop` returns `nil` while
// that thread is free to re-enter the guest. A silent §3 SP-2 breach, and the boundary counter left odd
// beside it.
//
// **What this slice changed is the trigger, not the mechanism.** The straight-line pairs are older than
// #602; what is new is that embedder code runs inside the interpreter's own call frames at all, so a panic
// can now originate inside a crossing and be recovered by the party that raised it. Before host functions
// a panic here meant an engine bug and process death, which has no state to corrupt afterwards.
//
// Filed with its four options rather than repaired in this slice, because every candidate either pays
// 0067's measured cliff or changes what an embedder observes:
// [#650](https://github.com/scttfrdmn/burroughs/issues/650).
//
// # Why the results are checked and the arguments are not
//
// The validator has already agreed the *guest* passes what the import declares, so the arguments on the
// stack are the declared parameters by construction. Nothing has agreed anything about what an
// embedder's Go function returns, and a host function returning two values where its type declares one
// would corrupt the caller's operand stack exactly as a wasm callee doing so would — which is why
// `invoke` checks its callee's arity too, and for the same reason.
func (in *Instance) callHost(h *hostFunc, st *stack) error {
	t := st.t
	// **The world comes from the thread and not from `in`**, and the two differ on a cross-instance
	// call: see `thread.world` for why the counter and the cancellation have to have one subject.
	w := t.world()
	if w == nil {
		// Unreachable as an engine fact — every thread that can reach a guest body was `register`ed or
		// `admit`ted, and `addLocked` is the one place membership is established — and reported rather
		// than dereferenced, per grave 0003's rule for an invariant a caller could break later. A panic
		// here would name the crash site instead of the invariant.
		return fmt.Errorf("%w: a host call on a thread no world admitted (engine invariant, "+
			"world.addLocked)", ErrNotValidated)
	}
	if err := w.beginHostCall(); err != nil {
		return err
	}
	// **One `defer`, doing two jobs, because a second one is a measured cliff** — ADR 0070, and 0067's
	// number is what makes the shape rather than the flag the interesting part. `embedderRunning` is true
	// across exactly `h.fn` and nowhere else, which is what makes the repair unable to fire on the
	// argument-marshalling error return below: on that path no crossing has been left open and no mark
	// set, and an unconditional repair here would take `blocked` to -1.
	embedderRunning := false
	defer func() {
		if embedderRunning {
			// `unmarkBlocked` and not `leaveBlocked`: no park on an unwinding thread, argued at that
			// function. `enterGuest` closes the excursion this frame opened, so the crossing count is
			// even again by the time `invokeIndex`'s own `defer leaveGuest()` runs above us.
			t.unmarkBlocked()
			enterGuest()
		}
		w.endHostCall()
	}()

	args, err := hostArgs(st, h.ft.Params)
	if err != nil {
		return err
	}

	// `in` and not `t`, and the neighbouring line is why the distinction is worth a comment: the world
	// three lines up comes from the *thread*, because cancellation follows the running agent, while
	// memory 0 comes from the *declaring instance*, because an index space belongs to the module that
	// wrote the import. `Caller`'s own comment argues both directions at length.
	mem, memErr := in.hostMemory()
	c := &Caller{ctx: t.context(), tid: t.threadID(), mem: mem, memErr: memErr}

	leaveGuest()
	t.enterBlocked()
	embedderRunning = true
	results, callErr := h.fn(c, args)
	embedderRunning = false
	t.leaveBlocked()
	enterGuest()

	if callErr != nil {
		return fmt.Errorf("%w: %w", ErrHostTrap, callErr)
	}
	return in.pushHostResults(st, h.ft.Results, results)
}

// hostArgs takes the declared parameters off the shared stack, innermost last.
//
// **Popped in reverse and filled in place**, because the stack's top is the *last* argument: reading
// forward and appending would hand the host its arguments backwards, which is invisible for a
// one-argument function and for any function whose parameters are all the same value.
func hostArgs(st *stack, params []binary.ValType) ([]Value, error) {
	args := make([]Value, len(params))
	for i := len(params) - 1; i >= 0; i-- {
		p := params[i]
		switch {
		case p.IsRef():
			if err := st.needRef(1); err != nil {
				return nil, err
			}
			args[i] = fromRef(st.popRef(), p)
		case p == binary.V128:
			if err := st.needNum(1); err != nil {
				return nil, err
			}
			hi, lo := st.popV128()
			args[i] = Value{Type: p, Bits: lo, Hi: hi}
		default:
			if err := st.needNum(1); err != nil {
				return nil, err
			}
			args[i] = Value{Type: p, Bits: st.popNum()}
		}
	}
	return args, nil
}

// pushHostResults puts the host's results on the shared stack in declaration order, having first
// checked that they *are* its results.
//
// # A result is checked exactly as `invokeIndex` checks a parameter, and for the same measured reason
//
// A host result travels **inward** — from outside the engine to the guest — which is the direction
// `invokeIndex`'s *parameters* travel, not the direction its results do. So this borrows that loop's
// discipline rather than the outward one's, and specifically it compares a reference by **subtyping**
// (`typeOfRef` + `matchRefType`) rather than by type identity. Identity is wrong in the two directions
// `invokeIndex` records as measured refusals of programs the reference runs: there is exactly one
// heaptype-free null, so a null spelled at any type serves any nullable reference result (grave #266);
// and a host reference's dynamic type is `(ref any)`, which a nullable `anyref` result admits and `==`
// never could.
//
// The non-null funcref refusal comes across too, ahead of `toRef`, for `Value.RefID`'s stated reason —
// a bare module-local function index arriving from outside the engine names no instance. It is worse
// here than at `invokeIndex`: there the index could at least be read in the callee's own module, and a
// host function has no module at all.
//
// # The count is checked before anything is pushed
//
// A wrong-arity host function leaves the stack exactly as it found it rather than half-written, because
// the caller's own operands are on that stack. The two loops are therefore not merged: checking and
// pushing in one pass would push result 0 and then discover result 1 is wrong.
func (in *Instance) pushHostResults(st *stack, want []binary.ValType, got []Value) error {
	if len(got) != len(want) {
		return fmt.Errorf("%w: declares %d result(s) and returned %d",
			ErrHostSignature, len(want), len(got))
	}
	refs := make([]ref, len(want))
	for i, w := range want {
		if w.IsRef() {
			if !got[i].Type.IsRef() {
				return fmt.Errorf("%w: result %d is %s and returned %s",
					ErrHostSignature, i, w, got[i].Type)
			}
			if w == binary.FuncRef && !got[i].Null {
				return fmt.Errorf("%w: result %d is a non-null funcref, which this boundary cannot "+
					"accept from outside the engine (see interp.Value.RefID)", ErrUnsupportedOp, i)
			}
			r := got[i].toRef(in)
			dyn, terr := typeOfRef(r, fmt.Sprintf("host function result %d", i))
			if terr != nil {
				return terr
			}
			if !matchRefType(dyn, castTarget(w, in.mod)) {
				return fmt.Errorf("%w: result %d is %s and returned %s",
					ErrHostSignature, i, w, dyn)
			}
			refs[i] = r
			continue
		}
		if got[i].Type != w {
			return fmt.Errorf("%w: result %d is %s and returned %s",
				ErrHostSignature, i, w, got[i].Type)
		}
	}
	for i, w := range want {
		switch {
		case w.IsRef():
			st.pushRef(refs[i])
		case w == binary.V128:
			// decision 0024: a v128's high half crosses through Value.Hi, never through Bits alone.
			st.pushV128(got[i].Hi, got[i].Bits)
		default:
			st.pushNum(got[i].Bits)
		}
	}
	return nil
}

// hostTypeIsLinkable reports why a host function's declared type cannot be linked, or "" if it can.
//
// The one refusal is a type-index-bearing reference type — `IsIndexed`, `binary.ValType`'s own
// predicate for the `(ref $t)` / `(ref null $t)` form. See `HostExtern` for why such a type cannot be
// resolved against anything here, and why refusing is a named limit rather than a defect.
func hostTypeIsLinkable(ft binary.FuncType) string {
	for i, p := range ft.Params {
		if p.IsIndexed() {
			return fmt.Sprintf("parameter %d is %s, and a host function has no type section for that "+
				"index to name (engine limit, decision 0069)", i, p)
		}
	}
	for i, r := range ft.Results {
		if r.IsIndexed() {
			return fmt.Sprintf("result %d is %s, and a host function has no type section for that "+
				"index to name (engine limit, decision 0069)", i, r)
		}
	}
	return ""
}

// hostTypeMatches reports whether a host function's declared type is the one an import declares —
// `importTypeMismatch`'s func arm's job, done here because that function cannot do it for a host
// extern at all: it reads both sides' type indices out of a *module*, and `Extern.typeSpace` returns
// nil for a host function, which it renders as *"a supplier with no defining module"*.
//
// **Structural equality, and it is deliberately stricter than `match_deftype`.** The reference's
// linker admits a supplier whose type is a *subtype* of what the importer declares — contravariant in
// the parameters, covariant in the results. Reproducing that here would mean running
// `validate.MatchDefType` with a synthetic type section on one side, which is the same
// index-against-whichever-module-is-at-hand move `hostTypeIsLinkable` refuses. So an embedder must
// declare the imported type exactly, and this is an **over-rejection named as one**: a host function
// declaring `(ref null func) -> []` against an import of `(ref func) -> []` is refused where the spec
// would accept it. No corpus vector is affected — the suite has no embedder-supplied functions — and
// the direction is the safe one, since an over-rejection reports itself and an over-acceptance links a
// function whose type the guest then trusts.
// hostImportMismatch reports why import `im` and host function `h` disagree, or "" if they match —
// `importTypeMismatch`'s func arm for the one supplier that has no module. Called from
// `InstantiateLinked`'s import loop, which explains there why it is a separate arm rather than a
// widening of that function.
//
// The two refusals in order, and the order matters because the second cannot be asked before the
// first: a host type naming a type index cannot be *compared* to anything, so it is refused for what it
// is before it is compared for what it says.
func (in *Instance) hostImportMismatch(im *binary.Import, h *hostFunc) string {
	if why := hostTypeIsLinkable(h.ft); why != "" {
		return why
	}
	if int(im.Index) >= len(in.mod.Types) {
		// #9's territory arriving at link time — a type index past the end of the type section. Said
		// as the layering debt it is rather than as a supplier's fault: nothing is wrong with the host
		// function, and blaming it for the importing module's index would be `tableFor`'s mistake.
		return fmt.Sprintf("declares type %d of %d, which this engine's validator (#9) would have refused",
			im.Index, len(in.mod.Types))
	}
	ct := &in.mod.Types[im.Index]
	if ct.Kind != binary.CompFunc {
		return fmt.Sprintf("declares type %d, which is a %s rather than a func", im.Index, ct.Kind)
	}
	if hostTypeMatches(ct.Func, h.ft) {
		return ""
	}
	// `expected …, got …` — eval.ml's `Link.error` wording, so the caller's sentinel plus this detail
	// reads as the reference's message. The importer's side is spelled by `speller` against its own
	// module, as the sibling arms do; the host's side is spelled from the bare types it declared, which
	// is all there is to spell.
	want := speller{mod: in.mod}
	return fmt.Sprintf("expected %s, got a host function %s",
		want.externFunc(im.Index), hostFuncTypeString(h.ft))
}

// hostFuncTypeString renders a host function's declared type in the reference's `func [params] ->
// [results]` shape.
//
// **Its own renderer rather than `speller.externFunc`, because that one takes a type *index*.** Every
// spelling in this package resolves indices against a module so a reader can follow them (grave #368's
// testimony half), and a host function's type has neither an index nor a module. So this prints the
// types directly — which is honest here for the same reason it would be dishonest there.
func hostFuncTypeString(ft binary.FuncType) string {
	one := func(ts []binary.ValType) string {
		parts := make([]string, len(ts))
		for i, t := range ts {
			parts[i] = t.String()
		}
		return "[" + strings.Join(parts, " ") + "]"
	}
	return fmt.Sprintf("func %s -> %s", one(ft.Params), one(ft.Results))
}

func hostTypeMatches(want, got binary.FuncType) bool {
	if len(want.Params) != len(got.Params) || len(want.Results) != len(got.Results) {
		return false
	}
	for i := range want.Params {
		if want.Params[i] != got.Params[i] {
			return false
		}
	}
	for i := range want.Results {
		if want.Results[i] != got.Results[i] {
			return false
		}
	}
	return true
}

// Close is contract §5 H-3's engine shutdown: terminal, cancelling, and waiting.
//
// **Terminal, with no `Resume` after it.** `Stop`/`Resume` are a pause and this is a teardown, which is
// the distinction that dissolved the apparent SP-4-versus-H-3 conflict: *"A pause must not disturb a
// blocked host call; a teardown must interrupt it"* (Scott, on the #646 review, recorded at #602). So
// this does not touch `stopReq`, does not park anything, and does not interact with a stop in progress
// beyond refusing to begin new host calls.
//
// **It waits on the calls, not on a timer.** Every member's context is cancelled, then this returns once
// every in-flight host call has returned — waited on a signal, because *a duration is not a completion
// signal* and a deadline here would report shutdown's completion at the granularity of the deadline.
// The consequence is stated on `Caller.Context`: an embedder who ignores cancellation makes this wait
// forever, and abandoning a running Go function is not something Go can do.
//
// Idempotent: a second `Close` returns nil, having nothing left to cancel. It returns an error only for
// what it cannot complete, which today is nothing — the signature is `error` because H-3's shutdown will
// grow a report (§6's loop, for one), and a method that starts as `func()` cannot gain one.
func (in *Instance) Close() error {
	w := &in.world

	w.mu.Lock()
	w.closed = true
	// Copied rather than walked outside the lock, because `admit` appends to this slice and a
	// concurrent `Spawn` would otherwise be a data race on the header. Cancelling happens outside
	// the lock: `context.CancelFunc` runs the context's own machinery, and a host function's
	// `select` waking while we hold `w.mu` would find `endHostCall` unable to take it.
	members := make([]*thread, len(w.members))
	copy(members, w.members)
	// A `Close` racing another `Close` must wait on the *same* channel rather than replace it, or
	// the first waiter's channel is dropped on the floor and never closed.
	if w.hostCalls > 0 && w.hostIdle == nil {
		w.hostIdle = make(chan struct{})
	}
	idle := w.hostIdle
	w.mu.Unlock()

	for _, t := range members {
		t.cancelCtx()
	}

	if idle != nil {
		// A closed channel rather than a re-checked count, for `world.resume`'s own stated reason:
		// *"a closed channel is the only release that cannot be missed by a thread that started
		// waiting after the close."* The channel is created under the same mutex `endHostCall`
		// *claims* it under — the close itself is outside both critical sections, because §4 B-MM-3
		// forbids holding an engine lock across a channel operation — so a call returning in the
		// window between the unlock above and this receive has already claimed the channel under the
		// lock and will close it, and this receive completes when it does.
		<-idle
	}
	return nil
}
