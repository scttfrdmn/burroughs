package interp

import (
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// Instance is a module ready to be invoked.
//
// **Not a full instantiation, and the name is the honest one available.** Contract §3's
// instantiation links imports, evaluates global initializers, and runs the start function; none
// of that is here. What *is* here as of 0015 is linear memory: allocated at its declared minimum
// and initialized from the module's active data segments.
//
// # Two kinds of failure, two channels (0015)
//
// The constructor used to promise it **could not fail**, on this reasoning, which was right
// about what it was defending and is preserved because of that:
//
//	An `Instantiate` returning an error would be a second place judging modules, and the
//	judgement it would be making is #9's.
//
// That intent survives intact — the interpreter never judges a module — but the promise was
// stated one step too strongly. Copying an active data segment past the end of its memory is
// not a judgement about the module; it is a **runtime event**, and `Trap` is the carrier built
// for events. So:
//
//   - **Verdicts belong to the validator, forever.** A global initializer that is not a constant
//     expression, an unlinkable import, a table whose element type disagrees: this package never
//     reports these, under any name. Where one is unavoidably reached before #9 exists, it comes
//     out as ErrNotValidated — the layering debt said out loud, not a spec verdict.
//   - **Traps belong to execution**, and instantiation *is* execution at time zero.
//
// The taxonomy is the suite's rather than this engine's, which is what settled it: `data1.wast`
// is 14 vectors of `assert_trap` wrapping a bare `(module …)`, every one expecting `out of bounds
// memory access`, with no invoke anywhere in the form. The oracle already distinguishes a module
// that is invalid from a module that traps while coming to life, so the design has to answer a
// question the judge is asking.
//
// Enforced by the return type rather than by this comment: Instantiate returns `*Trap`, so a
// verdict cannot travel through it even by mistake.
type Instance struct {
	mod *binary.Module

	// mems is the memory index space, in index order — **imports first, then definitions**,
	// which is the space's shape and not a convenience. A slot is nil when there is nothing
	// to put in it: an imported memory that no supplier filled, or a declared one whose
	// allocation failed for a reason that is #9's rather than a trap's. See Instantiate on why
	// a nil slot beats a shorter slice.
	//
	// **The import slots are reserved rather than omitted, and the difference is 22 vectors.**
	// The first draft sized this `len(m.Memories)` and argued in this comment that a module
	// importing a memory "has no memory to allocate and its accesses reach memoryFor's index
	// check" — which is true only if the import consumed no index. It consumes one, so
	// `memory.size $mem1` in `memory_grow.wast` read $mem3 and returned 3 pages instead of 2:
	// not an unimplemented import reported honestly, a *wrong answer* about a different
	// memory. The nil-slot rule was already written down one paragraph over for allocation
	// failures; it stopped at the import boundary because nothing had crossed it yet.
	mems []*memory

	// tables is the table index space, in index order — imports first, then definitions, for
	// mems's reason and by the same measured lesson. Nothing new is being decided here: the
	// nil-slot rule, the reserved-not-omitted rule, and the reason both are stated on the field
	// rather than at the constructor all transfer intact, which is what makes this a
	// three-line addition rather than a second design.
	tables []*table

	// globals is the global index space, in index order — imports first, then definitions, for
	// mems's reason and by the same measured lesson, which transfers without new argument (see
	// `binary.ImportedGlobals`). Nothing new is decided here either: nil slot, reserved not
	// omitted, reason stated on the field.
	//
	// A slice of pointers rather than values, and that *is* a decision: `global.set` mutates a
	// slot in place, so a value slice would need indexing at every write and `globalFor` could
	// not hand out something writable. The nil-slot convention needs the pointer anyway.
	globals []*global

	// elems and datas are the element and data segments' *runtime* instances, in index order
	// and one per segment in the image — see segment.go for why the image is not enough, and
	// for the observable `bulk.wast` vector that makes the drop state load-bearing.
	//
	// **No import offset and no nil slots**, and neither is an oversight. There is no import
	// kind for a segment — `ast.ml`'s `externtype` is func/table/memory/global/tag — so these
	// spaces are module-local and every index in them names something the module declared. The
	// mems/tables nil-slot convention exists for imports and failed allocations, and a segment
	// has neither: `allocElem` can fail (an element expression this phase cannot evaluate), and
	// that failure goes to `deferred` with the *slot filled* by an empty instance, so a later
	// `table.init` reads an empty segment rather than dereferencing nil. That is a weaker
	// guarantee than the tables' and it is stated rather than assumed: the empty instance makes
	// the arm answer "out of bounds" where the truth is "unevaluated", which `deferred` carries
	// and `Deferred()` reports.
	elems []*elemInstance
	datas []*dataInstance

	// funcs is the *imported* function range only — length `ImportedFuncs()`, indexed by
	// function index directly, with a nil slot for an import nothing supplied.
	//
	// **Asymmetric with mems/tables/globals, and the asymmetry is the index space's own.** For
	// those three the imports and the definitions share one slice because both need a runtime
	// object. A defined function needs none: `binary.Module.DefinedFunc(idx)` already resolves
	// the definition side from the image, subtracting the import offset itself. So a
	// full-length slice here would be `ImportedFuncs()` entries followed by nils that nothing
	// reads, and the nil would then mean two different things — "not linked" below the offset
	// and "look in the module" above it. Half a slice with one meaning beats a whole slice
	// with two.
	funcs []*Extern

	// tags is the tag index space, in index order — imports first, then definitions, for
	// mems's reason and by the same measured lesson (0022 §3): reserved-not-omitted, not a
	// shorter slice. Unlike funcs, a defined tag *does* need a runtime object — a tag's
	// identity is its allocation (`tagInst`, not a module-relative index), matching
	// `mems`/`tables`/`globals`'s shape rather than `funcs`'s, since a tag has no body a
	// module-relative lookup could resolve lazily the way `DefinedFunc` does.
	tags []*tagInst

	// deferred holds the validation-shaped failures instantiation met and could not report,
	// because 0015's trap channel may not carry a verdict.
	//
	// **Retained rather than dropped, and read at the point of use** (memoryFor): a nil
	// memory slot with no reason attached would make an access report "memory 0 of 1" when
	// the truth is "this memory declared min above max", which is the engine being vague
	// about its own input. Every one of these becomes unreachable once a validator rejects
	// such a module ahead of instantiation — the same declared-and-tracked shape as
	// ErrNotValidated itself, and the same condition, which is a state of the code rather
	// than #9's closure (ADR 0043).
	deferred error

	// nextTID hands out contract §2 T-1 thread ids, monotonic from 1 — see `InstantiateLinked`,
	// which takes the first.
	//
	// **Atomic for the second incrementer, and today there is exactly one.** Instantiation takes id
	// 1 and nothing else calls this, so no race exists in the landed tree and this comment does not
	// claim one. It is typed for T-2's requirement rather than retyped when that arrives: T-2
	// forbids a main-thread special case, so once spawn lands *any* thread may spawn and two can
	// race for an id — and a field's type flipping back and forth across two PRs is worse than a
	// word of explanation here.
	nextTID atomic.Uint64

	// host is the thread the *host's* calls run on — the thread handed to every stack this engine
	// creates (`runConst`, the start function, `invokeIndex`).
	//
	// **Named `host` and not `main`, because T-2 forbids a main thread and this is not one.** It
	// carries no privilege, no special id, and nothing would distinguish it from a spawned thread
	// except which side created it. Calling it `main` would assert the special case T-2 rules out,
	// in the one channel that gets no review, and the field would be cited later as evidence the
	// engine has one.
	//
	// **A value and not a pointer, and the reason is *not* a measurement — that is worth saying,
	// because for three rounds it looked like one.** A `*thread` here was measured at +2.73% and then
	// +3.10% on `scanbench`'s `Instantiate/funcs=1/openers=1` row, against decision 0050's
	// pre-registered 2% ceiling, and by-value was adopted as the repair. Both figures were artifacts
	// of the measurement protocol: the arms ran sequentially, so run order was a confounder perfectly
	// correlated with the arm (grave #552). Interleaved at `-count=1` across ten rounds the two forms are
	// indistinguishable on that row and on every other (see 0050's result section). So this field's
	// shape is a design choice with the performance question answered *neutral*: it rides `Instance`'s
	// own allocation, so there is one fewer object to allocate, and it cannot be nil on an instance
	// the constructor built. The propagation sites take `&in.host`, so what `stack` carries is still
	// one pointer either way.
	//
	// Non-nil by construction on any instance `InstantiateLinked` built, which is every instance
	// outside this package's own tests, so `stack.t` is set from the first instruction and #515's
	// safepoint check does not have to treat a threadless stack as a live state. That forecast is
	// now landed and holds: `poll` still returns nil on a nil receiver, but the path exists for
	// stacks this package's own tests build by literal and not for anything a caller can reach.
	host thread

	// world is contract §3 SP-1's stop-the-world state — `Stop`, `Resume`, and the members every
	// safepoint poll parks into. See `world` in safepoint.go for why its extent is one instance and
	// why that is a stated limit rather than the end state.
	//
	// A value for `host`'s reason: it rides this allocation, `register` takes `&in.world`, and an
	// instance whose world could be nil would make every poll's fast path answer a question about
	// the engine's own construction.
	world world
}

// Instantiate allocates a module's memories and copies its active data segments in.
//
// The module is retained, not copied: `binary.Module`'s payloads already alias the caller's
// image (the decoder's in-place posture), so a copy here would be a second aliasing of the same
// bytes under the illusion of ownership. The segments' bytes are copied, because a memory is
// mutable and the image is not.
//
// **Returns `*Trap`, never a bare error** — 0015's channel split, in the signature. A caller
// getting a non-nil trap has a module that came to life and died doing it, which is exactly what
// `assert_trap` wrapping a module form asserts.
func Instantiate(m *binary.Module) (*Instance, *Trap) {
	in, trap, err := InstantiateLinked(m, nil)
	if err != nil {
		// Unreachable with a nil resolver: the only error `link` produces is a kind
		// mismatch, and nothing can mismatch when nothing is supplied. Joined onto
		// `deferred` rather than dropped or panicked on, because an error constant with no
		// reachable path is a missing check wearing a disguise (grave 0003) — and a
		// `//nolint`-worthy panic here would assert a property of a *sibling function* that
		// a future arm could falsify silently.
		//
		// It cannot reach `in`, which is nil on this path, so it is reported by the only
		// channel this signature has left.
		return nil, &Trap{Reason: "link failed with no imports supplied: " + err.Error()}
	}
	return in, trap
}

// build allocates and initializes an instance whose import slots are already filled.
//
// **Split out of Instantiate rather than duplicated**, so the linked and unlinked paths
// cannot disagree about the reference's evaluation order — globals before tables before
// memories, elements before data, allocation before copying. That ordering is load-bearing
// four separate ways (see the comments below, each with the vector that proves it), and a
// second copy of it is four opportunities to drift.
func (in *Instance) build() *Trap {
	// §4 B-MM-1, at the enclosing function of the start-function `stack` literal below — the
	// interpreter is entered here, so the transition is here (`boundary.go`, decision 0052, #516).
	enterGuest()
	defer leaveGuest()

	m := in.mod
	memOff, tabOff := m.ImportedMems(), m.ImportedTables()
	globOff := m.ImportedGlobals()
	// **Tags first, ahead of even globals** — `eval.ml:1310-1319`'s own fold order
	// (`init_tag` before `init_func`/`init_global`/`init_table`/`init_memory`), and it needs
	// nothing from the loops below: a tag's allocation is pure, from its declared type alone,
	// with no initializer to sequence against an earlier tag or an earlier global (0022 §3).
	// A new, small loop rather than an insertion into the interleaved global/table/memory
	// one below — that loop's three concerns already interleave for a real dependency
	// (globals reading earlier globals); a tag has no such dependency to thread through it.
	if err := in.newTags(); err != nil {
		if t := asTrap(err); t != nil {
			return t
		}
		in.deferred = errors.Join(in.deferred, err)
	}
	// One slot per memory *index*, filled positionally and **never skipped**: the imported
	// memories first — already filled by `link` if a resolver supplied them, still nil if
	// nothing did — then the defined ones at the offset the index space gives them. A failed allocation likewise leaves a nil slot rather than
	// shortening the slice, because appending only the successes would shift every later
	// memory's index — the same defect `Module.Types` keeps struct and array slots to avoid,
	// and one no board could see, since the affected vectors are ones the suite expects to
	// pass. That last clause was written before the import offset was measured, and the
	// measurement is what made it concrete rather than cautionary: 22 vectors, all "passing"
	// with the wrong memory's answer.
	// **Globals first, and the position comes from the reference's fold rather than from
	// convenience.** `eval.ml:1310-1318` runs `init_global` *before* `init_table` and
	// `init_memory`, and it matters for a reason no ordering of the other three has: a global's
	// initializer is a const-expr that may read an *earlier global*, and a table's or memory's
	// segment offset may read a global too. So globals must be complete before anything else is
	// evaluated, and each global must see the ones below it.
	//
	// That second half is why this loop fills `in.globals[globOff+i]` as it goes rather than
	// building a slice and assigning at the end: `newGlobal` evaluates against `in`, so the slot
	// for global N must be visible while global N+1's initializer runs.
	//
	// **Falsified rather than asserted, and the prediction was wrong in the engine's favour.**
	// This comment first said an allocate-then-evaluate engine "would answer 0 for `(global i32
	// (global.get 0))`" — a wrong value. Running that mutation says otherwise: the slot is still
	// nil when the next initializer reads it, so `globalFor`'s nil-slot arm fires and the module
	// reports `global 0 was declared but not initialized` through the deferred channel. So the
	// nil-slot convention converts what would have been a silent wrong answer into a loud one,
	// which is the convention paying for itself in a place it was not designed for. Recorded
	// because the wrong version was the plausible one: a reader checking the code against the
	// claim would have found agreement, which is the defect-stated-as-the-rule shape.
	for i := range m.Globals {
		g, err := in.newGlobal(m.Globals[i])
		if err != nil {
			if t := asTrap(err); t != nil {
				return t
			}
			in.deferred = errors.Join(in.deferred, err)
			continue
		}
		in.globals[globOff+i] = g
	}
	// **After the globals loop, which is `eval.ml:1314-1315`'s order and is now load-bearing twice
	// over.** It always mattered for the reason the paragraph above gives — a global's initializer
	// reads earlier globals — and as of #419 a *table's* initializer is evaluated too, against the
	// same partially built instance, so `(global funcref (ref.func $f)) (table 1 funcref
	// (global.get 0))` needs global 0's slot filled before this loop runs. Moving these two loops
	// past each other answers that module with a nil-slot report instead of a table of `$f`.
	for i := range m.Tables {
		tab, err := in.newTable(m.Tables[i])
		if err != nil {
			if t := asTrap(err); t != nil {
				return t
			}
			in.deferred = errors.Join(in.deferred, err)
			continue
		}
		in.tables[tabOff+i] = tab
	}
	for i := range m.Memories {
		mem, err := newMemory(m.Memories[i])
		if err != nil {
			if t := asTrap(err); t != nil {
				return t
			}
			// A verdict-shaped failure, which cannot travel this channel (0015). It is
			// **retained, not dropped**: silent degradation is a skip one step quieter,
			// and a nil slot with no recorded reason would make the eventual access
			// report a missing memory instead of the reason it is missing.
			in.deferred = errors.Join(in.deferred, err)
			continue
		}
		in.mems[memOff+i] = mem
	}
	// **Elements before data, which is the reference's order** — `eval.ml:1316-1317` builds
	// `es_elem @ es_data @ es_start` and evaluates the concatenation, so every active element
	// segment is copied before any data segment is.
	//
	// It is observable, and *not* by the mechanism first written here. The plausible-sounding
	// claim was that a module whose table and memory segments both overrun would report the
	// table's trap, so a data-first engine would quote the wrong event — measured, and there are
	// **zero** such vectors: of the six `assert_trap (module …)` forms holding both an `(elem` and
	// a `(data`, two trap on the elem, two on the data, two on a start function, and none on
	// both. That comment would have cited a discriminator the corpus does not contain.
	//
	// What does discriminate is the **persisted side effect**: `linking.wast:413` has an
	// in-bounds elem at index 7 and an out-of-bounds data segment, traps on the memory, and is
	// followed by `(assert_return (invoke $Mt "call" (i32.const 7)) (i32.const 0))` — the table
	// write survives the failed instantiation, which is only true if it happened first. A
	// data-first engine passes the trap vector and fails the line after it.
	//
	// Both halves of that are the same lesson from opposite sides: the order is load-bearing, and
	// the reason it is load-bearing had to be looked up rather than reasoned out.
	//
	// **Allocation is a separate pass from copying, which is also the reference's shape and is
	// load-bearing for a second reason.** `init` runs `init_list init_data` and `init_list
	// init_elem` over *every* segment (`eval.ml:1317-1318`) and only then evaluates
	// `es_elem @ es_data @ es_start`, so all segment instances exist before the first copy. Merging
	// the two passes would make an active segment's instance unobservable — `run_elem` drops it
	// immediately after copying, so there would be nothing for the *next* segment's failure to
	// leave behind, and more to the point a `table.init` naming segment 3 from inside segment 1's
	// offset expression would find an unallocated slot. Two passes, in the reference's order.
	for i := range m.Elems {
		seg, err := in.allocElem(&m.Elems[i])
		if err != nil {
			if t := asTrap(err); t != nil {
				return t
			}
			// The slot is filled with an *empty* instance rather than left nil, per the field's
			// comment: a later `table.init` then reports out-of-bounds instead of panicking, and
			// the real reason travels on `deferred`.
			in.deferred = errors.Join(in.deferred, err)
			seg = newElemInstance(nil)
		}
		in.elems[i] = seg
	}
	for i := range m.Datas {
		in.datas[i] = newDataInstance(m.Datas[i].Init)
	}
	for i := range m.Elems {
		if err := in.runElem(i, &m.Elems[i]); err != nil {
			if t := asTrap(err); t != nil {
				return t
			}
			in.deferred = errors.Join(in.deferred, err)
		}
	}
	for i := range m.Datas {
		if err := in.runData(i, &m.Datas[i]); err != nil {
			if t := asTrap(err); t != nil {
				return t
			}
			in.deferred = errors.Join(in.deferred, err)
		}
	}
	// **The start function, last, after every segment has been copied** — `es_elem @ es_data @
	// es_start` (`eval.ml:1325`), and the position is the whole reason a start section is worth
	// having: `start.wast:20`'s module has `(memory (data "A"))` and a start function that increments
	// byte 0 three times, then asserts `(get) = 68` at :44 — 65 for the `"A"` plus three. A start that
	// ran before the data copy would read zero, increment a byte the copy then overwrites, and the
	// module would answer `65` — the `"A"` alone — which is a *plausible* wrong answer rather than a
	// crash. Measured, not asserted: moving this block above the data loop costs 8 board rows in the
	// default lane and 11 with every gate on, and the bill in `internal/validate/start_test.go` names
	// them (mutation M6).
	//
	// **`in.call` rather than `in.invokeIndex`, because the reference's `run_start` is literally
	// `[Call x]`** (`eval.ml:1294-1296`): a wasm call inside the instance's own config, not a host
	// boundary crossing. The two differ where it is observable — `invokeIndex` applies the boundary's
	// argument and frame-ceiling checks and would report a missing export by name — and the import
	// crossing this needs (`start.wast:94` is `(start $print)` on an *imported* function) is
	// `resolveCall`'s, which `call` already goes through.
	//
	// Depth 0, so the start function gets a full exhaustion budget minus its own frame. That is one
	// frame less than a boundary `Invoke` grants and it is the reference's shape rather than a
	// rounding: `Call x` is an administrative instruction at the top of the config, so the start
	// function's frame is the first one pushed.
	//
	// The empty stack is the config's `[]` operand stack. A `[] -> []` start leaves it empty, and
	// nothing reads it afterwards either way — the validator's `check_start` is what makes the arity
	// true (`moduleStart`), and this path does not depend on having run it.
	if m.HasStart {
		// `t: &in.host` — propagation site 2 of 3 (decision 0050). The start function runs on the
		// host's thread by definition: no guest code has run yet, so nothing could have asked for a
		// second thread even once T-1's spawn lands.
		if err := in.call(m.Start, &stack{t: &in.host}, 0); err != nil {
			if t := asTrap(err); t != nil {
				return t
			}
			in.deferred = errors.Join(in.deferred, err)
		}
	}
	return nil
}

// Deferred reports the failures instantiation met that could not travel the trap channel, or nil.
//
// **Exported because a caller can otherwise be told "nothing went wrong" when something did.**
// A trap answers "this module died coming to life"; a nil trap is *not* the same claim as "this
// module came to life completely". Between them sits the case this accessor exists for: an active
// data segment that could not be copied because its target memory is imported and nothing
// supplied it. Instantiation cannot trap for that — the reason is not a runtime event — and it cannot
// return a verdict either (0015), so the instance comes back usable with the shortfall recorded.
//
// Found on the board, which is the only reason it is exported: `data1.wast`'s :80, :117 and :136
// wrap modules whose data segments target imported memories, and all three were scored "the
// module instantiated without trapping" — true, unhelpful, and naming no missing component. With
// this the bucket names linking (contract §3), which is what a work plan is for.
//
// It is *not* an error channel in disguise: an accesses to a memory whose slot is empty still
// reports the reason at the point of use (memoryFor). This is for a caller that needs to know
// whether the instance is complete before deciding what a nil trap means.
func (in *Instance) Deferred() error { return in.deferred }

// asTrap extracts the trap in err, or nil if err is not one.
//
// The one place 0015's channel split is enforced, so that "traps travel, verdicts do not" is a
// single predicate rather than a convention repeated at each site.
func asTrap(err error) *Trap {
	var t *Trap
	if errors.As(err, &t) {
		return t
	}
	return nil
}

// Module returns the instance's module.
func (in *Instance) Module() *binary.Module { return in.mod }

// Trap is a wasm trap: the module executed correctly and the *program* went wrong.
//
// **A distinct type from every other error this package returns, because the suite scores the
// two in opposite directions.** `assert_trap` wants a trap and `assert_return` wants a value, so
// an engine that reported "integer divide by zero" the same way it reports "this opcode has no
// arm yet" would make 4963 assert_trap vectors indistinguishable from 4963 engine gaps — the
// fail-column dilution decision 0010 exists to prevent, one layer down.
//
// The Reason strings are the spec's own trap texts, because that is what `assert_trap`'s second
// argument matches against. They are *testimony* in the sense the doctrine means: a trap
// reporting the wrong reason is right about the verdict and wrong about the evidence, and the
// suite reads far enough to catch it.
type Trap struct {
	Reason string
}

// Error renders the reason first and the word "trap" after it, which is the reverse of Go's
// context-prefix convention and is ADR 0045's decision: `assert_trap` matches its expected text by
// **prefix** (`runner.ml:498-501`), so a leading `"trap: "` made 4262 default-lane vectors pass under
// the harness's then-looser substring rule for a reason the reference would have refused. The Reason is
// the spec's own phrase, so putting it at position 0 is what makes the two rules agree.
//
// **The public `burroughs.Trap` deliberately renders the other way** — `"trap: " + Reason`, the Go
// idiom — because the reference's domain is this package and the root package's invariant is that the
// *Reason* is the engine's, not that its rendering is. See burroughs.go's Error, which says the same
// thing from the other side.
func (t *Trap) Error() string { return t.Reason + " (trap)" }

// The trap reasons the numeric core can produce, spelled as the spec spells them.
//
// Three of them, and that is the complete set for arithmetic — every other trap in the suite
// (`out of bounds memory access`, `undefined element`, `uninitialized element`, `indirect call
// type mismatch`) belongs to a construct this package does not execute yet, and `unreachable`
// is declared beside the instruction that raises it (exec.go). Named constants rather than
// inline strings so that a fourth one arriving is a declaration rather than a literal in a
// switch arm.
//
// It said "Four of them" over a block of three, having counted `unreachable` and then not
// declared it here — a comment asserting a property of its own declaration block, which is the
// cheapest kind to check and was not checked. Corrected by counting the vars.
var (
	trapDivByZero   = &Trap{Reason: "integer divide by zero"}
	trapIntOverflow = &Trap{Reason: "integer overflow"}
	trapBadConvert  = &Trap{Reason: "invalid conversion to integer"}
)

// maxFrameLocals is the most locals this engine will build a frame for: 2^24, so 128 MiB of
// slots at eight bytes each.
//
// **An engine limit with a stated basis, which is the difference between a bound and a round
// number.** The spec's own limit is 2^32 (the decoder's `too many locals`), and honouring it
// literally means a well-formed module can demand a 32 GiB frame — the execution-side half of
// grave #138, which the decoder no longer pays and which does not vanish by being moved. The
// figure is chosen so that the refusal cannot be mistaken for a policy about reasonable code:
// 16.7 million locals in one function is four orders of magnitude past anything a compiler
// emits, so a module refused here was constructed to be refused.
//
// It is deliberately **not** derived from available memory. A ceiling that varies by host
// makes the engine's verdict depend on where it runs, and a module that executes on the dev
// box and is refused in CI is the least debuggable failure this package could offer. Fixed,
// stated, and the same everywhere.
//
// When #9 lands this stays: the validator's job is to reject invalid modules, and this module
// is valid. It is an engine capability limit, which is why it reports ErrUnsupported.
const maxFrameLocals = 1 << 24

// ErrUnsupportedOp is the engine saying it has no arm for an instruction.
//
// **Not a verdict on the module, and the distinction is the whole reason it is a separate
// sentinel from Trap.** The module is well-formed and the instruction is a real one; what is
// missing is engine, and the honest report names the engine's gap. That makes the board's
// failure bucket a work plan keyed by opcode — `interp: no arm for opcode 0xfd 0x03` names SIMD,
// `0x3f` names memory — which is the bucketed-failures discipline pointed at this layer.
//
// It is reported when the instruction is *reached*, never by scanning a body in advance. A
// pre-scan would refuse a function over an instruction on a path that never executes, which
// would be an engine gap masquerading as a module property; and it would make the pass column
// claim less than the engine actually did. With no control flow in the numeric core the two are
// the same set today, which is exactly when the cheaper-looking choice should be inspected
// rather than taken.
var ErrUnsupportedOp = errors.New("interp: no arm for opcode")

// ErrNotValidated is the interpreter declining to execute something #9 would have rejected.
//
// **A declared layering debt, not a validation verdict** (#6's declared-and-tracked ruling). The
// validator does not exist, so a body reaching this package can index a local that is not there
// or pop a stack that is empty — and the choice is between panicking, checking, and being wrong.
// This is the check: it returns rather than panics, it says which invariant failed, and it says
// in this comment that every one of its call sites becomes unreachable **when a validator refuses
// these modules before they reach this package** — a state of the code, not #9's closure, which is
// a bookkeeping event this sentinel survives (ADR 0043 amended G-1's retirement condition for
// exactly that reason, and this comment is the authority G-1's own clause used to cite). A fuzz
// target over decoded-but-invalid modules is the reason it exists at all, because such a module
// is exactly what the decoder accepts and the validator would not.
//
// What it must never become is a spec verdict: `type mismatch` is the validator's string, and
// reporting it from here would put #9's answer in a place #9 cannot be tested from.
var ErrNotValidated = errors.New("interp: module reached the interpreter unvalidated")

// ErrUnsupported is an engine feature the module legitimately asked for and this phase does not
// implement.
//
// **The third category, and it exists because the first two would have lied.** A module importing
// a memory is well-formed (not ErrNotValidated) and the instruction reaching for it is a real
// arm that this engine has (not ErrUnsupportedOp); what is missing is the *supplier*. Reporting
// either sibling would have named the wrong gap — one blames the module, the other blames a
// table — and the board's buckets are a work plan only while each key names the thing actually
// missing.
//
// **The gap this names narrowed and did not close**, which is why the category survived the
// linker. It said "what is missing is *linking*, which is contract §3 and v2-or-later work",
// and a script-level registry falsified the second half of that sentence: `InstantiateLinked`
// fills the slot, so the reached-import case is now specifically an import *nothing supplied*
// — an unregistered module, or a supplier whose own instantiation failed. Still this category
// and not ErrNotValidated, because such a module is well-formed and the engine's shortfall is
// a component it does not have (a host-import surface, §3) rather than a fault in the module.
//
// Like ErrUnsupportedOp it is reported when the feature is *reached*, so a module that imports a
// memory and never touches it still runs.
var ErrUnsupported = errors.New("interp: feature not implemented in this phase")

// Invoke calls an exported function by name.
//
// Argument checking is by type and arity against the declared functype, and it happens *before*
// the frame is built — the boundary is where the static knowledge stops (see Value), so it is
// also where the check belongs. A host passing an i64 where an i32 is declared gets an error
// naming both types rather than a silently truncated slot.
//
// Results come back in stack order, which is declaration order: wasm pushes results left to
// right, so popping fills the slice from the end. Getting that backwards is invisible for the
// 12799 single-result vectors in the corpus and wrong for the 1188 multi-result ones, which is
// the shape of defect a majority-of-the-corpus check scores green.
func (in *Instance) Invoke(name string, args ...Value) ([]Value, error) {
	idx, ok := in.exportedFunc(name)
	if !ok {
		return nil, fmt.Errorf("interp: no exported function %q", name)
	}
	return in.invokeIndex(idx, name, args)
}

// Global reads an exported global's current value. `Invoke`'s sibling for the other action the
// script grammar has (#323): in wast an *action* is `invoke | get`, and this is `get`'s engine end.
//
// **No delegation for a re-exported import, where `invokeIndex` needs one, and the asymmetry is a
// property of what the two things *are*.** A function export resolves to an index whose body lives
// in the supplying instance, so the call has to travel there; a global export resolves to storage,
// and `link` assigns the supplier's own `*global` into `in.globals[slot]` (link.go's
// `ExternGlobal` arm), so the pointer this reaches through already *is* the supplier's object. A
// re-export chain therefore needs no hop — reading through it and reading at the definition are
// the same read of the same slot, which is also why `global.mod` sits on the allocation rather
// than on the Extern (#368).
//
// Results come back as one Value built by `global.value`, sharing `global.get`'s layout dispatch
// so that a fourth storage shape cannot be right for the interpreter and wrong here — grave #239
// was that split with `v128`, on the read-back half specifically.
func (in *Instance) Global(name string) (Value, error) {
	// §4 B-MM-1, at the second of the two sites that run no guest code: this reads a global's
	// storage directly, and a stale read is exactly what the acquire edge forbids (`boundary.go`,
	// decision 0052, #516).
	enterGuest()
	defer leaveGuest()

	idx, ok := in.exportedGlobal(name)
	if !ok {
		return Value{}, fmt.Errorf("interp: no exported global %q", name)
	}
	// globalFor rather than an inline index, for its own comment's reason: its two failure modes
	// — an import nothing supplied (§3, ErrUnsupported) and a declared global whose initializer
	// deferred (ErrNotValidated) — are both reachable from here, and this is precisely the caller
	// that would half-remember them. `what` names the holder of the index the way the other
	// callers do: "the export" sends a reader to their export section rather than to a body.
	g, err := in.globalFor(fmt.Sprintf("the export %q", name), uint64(idx))
	if err != nil {
		return Value{}, err
	}
	return g.value(), nil
}

// invokeIndex is Invoke past the name lookup: the boundary call to a function *index*.
//
// **Split out because an exported import makes the name and the index belong to different
// instances.** The name is the importer's, the body is the supplier's, and every check below —
// arity, parameter types, the frame ceiling — is a property of the *body*. So the delegation has
// to happen after the name resolves and before anything is checked, which is precisely this seam.
// `name` travels along for the error messages only: a host that asked for `"call"` should be told
// about `"call"`, not about whatever index it turned out to be two instances away.
func (in *Instance) invokeIndex(idx uint32, name string, args []Value) ([]Value, error) {
	// §4 B-MM-1. Here rather than in `Invoke` because this is the enclosing function of the
	// `stack` literal below, so the structural control's parsed population covers it; the
	// delegation to a supplier's `invokeIndex` therefore crosses once per hop in a re-export
	// chain, which is granularity and not double-counting (`boundary.go`, decision 0052, #516).
	enterGuest()
	defer leaveGuest()

	fn, ok := in.mod.DefinedFunc(idx)
	if !ok {
		// **An exported *import*, which is a name this module passes through** — `Mt` in
		// `linking.wast` exports `call` and re-exports imports beside it, and a script may
		// invoke either. Resolved by delegating to the supplier's own boundary rather than by
		// building a frame here: the callee's parameter checks, its frame ceiling, and its
		// result ordering are all the supplier instance's, and a second copy of that logic on
		// this path is a second place for it to be wrong.
		//
		// The recursion terminates for `resolveCall`'s reason: a supplier is instantiated
		// before its importer, so a re-export chain cannot cycle. (It cited `callImport`, which
		// 0026's resolution/entry split replaced — same argument, new home.)
		ext, ierr := in.importedFunc(idx)
		if ierr != nil {
			return nil, ierr
		}
		return ext.owner.invokeIndex(ext.fnIdx, name, args)
	}
	ft, err := in.funcType(fn)
	if err != nil {
		return nil, err
	}
	if len(args) != len(ft.Params) {
		return nil, fmt.Errorf("interp: %q takes %d arguments, got %d", name, len(ft.Params), len(args))
	}
	// The frame's locals: parameters first, then the declared locals zeroed. `newFrame`
	// (value.go) allocates the parallel reference array lazily, per its own doc comment.
	//
	// **This is where the flat local vector is paid for, and where it is bounded** (#138). The
	// decoder retains the wire form — `(count, valtype)` runs — so `len(fn.Locals)` counts
	// *groups* and the flat total comes from `TotalLocals`. Getting that wrong is a live
	// hazard rather than a hypothetical: `len` compiles, is off by the compression ratio, and
	// would size the frame for three groups where a body declares a million locals.
	//
	// The bound is this engine's, not the spec's. A body declaring 0xFFFFFFFE locals is a
	// well-formed module the reference runs, and eight bytes a slot makes its frame 32 GiB —
	// so the refusal is ErrUnsupported (an engine limit this phase has), never
	// ErrNotValidated (which would blame the module) and never a trap (which would claim a
	// spec outcome the spec does not give). The ceiling is deliberately generous: it exists to
	// stop an allocation no host can serve, not to express a policy about reasonable
	// functions.
	total := fn.TotalLocals() + uint64(len(ft.Params))
	if total > maxFrameLocals {
		return nil, fmt.Errorf("%w: %q declares %d locals, and this engine's frame ceiling is %d",
			ErrUnsupported, name, total, maxFrameLocals)
	}
	locals := newFrame(total, ft.Params, fn.EachLocal)
	for i, p := range ft.Params {
		if p.IsRef() {
			// **A reference argument is checked by *subtyping*, not by type identity, and that is a
			// transcription rather than a leniency.** `eval.ml:1159-1169`'s `invoke` is
			//
			//	if not (List.for_all2 (fun v -> Match.match_valtype [] (type_of_value v)) vs ts1) then
			//	  Crash.error at "wrong types of arguments";
			//
			// so the reference compares the argument's **dynamic** type against the parameter's
			// declared one through the same `Match` relation `ref.test` evaluates — which this engine
			// has, in `typeOfRef`/`matchRefType`, and was not consulting here.
			//
			// Identity was wrong in two measured directions, both of them refusals of programs the
			// reference runs:
			//
			//   - **A null.** `type_of_value NullRef` is `(Null, BotHT)` (`value.ml:112`) and
			//     `matchHeapType`'s arm 11 makes bottom a subtype of everything, so one null value
			//     serves every nullable reference parameter — grave #266's law (there is exactly one
			//     heaptype-free null) reaching the host boundary. `extern.wast:43` is the vector:
			//     `(assert_return (invoke "externalize" (ref.null any)) (ref.null extern))` passes a
			//     null the harness spells at one type into a parameter declared at another, and the
			//     identity check reported *`"externalize" parameter 0 is (ref null any), got
			//     externref`* — an engine refusing a value on the strength of a distinction the
			//     reference does not have.
			//   - **A non-null reference under an abstract parameter.** A host reference's dynamic
			//     type is `(ref any)` (`script.ml:80`) and `extern.wast:42` hands one to an `anyref`
			//     parameter; `matchNull` admits the non-nullable under the nullable, so the reference
			//     accepts it and identity never could.
			//
			// It does not *loosen* anything the corpus relies on: an externref against a `funcref`
			// parameter still fails (`extern` and `func` are disjoint hierarchies, `matchHeapType`
			// arm 12), and a non-nullable parameter still refuses a null (`matchNull`, which is the
			// whole content of `ref.test (ref $t)` answering 0 on one).
			if !args[i].Type.IsRef() {
				return nil, fmt.Errorf("interp: %q parameter %d is %s, got %s", name, i, p, args[i].Type)
			}
			// The funcref scope refusal comes first, ahead of `toRef`: it is Value.RefID's own
			// stated boundary rather than a type error, and the shape it declines is one `toRef`
			// would resolve against the *caller's* instance on the way to a check that would then
			// pass. externref (null or not) and a null funcref both convert cleanly.
			if p == binary.FuncRef && !args[i].Null {
				return nil, fmt.Errorf("%w: parameter %d of %q is a non-null funcref, which this "+
					"boundary cannot accept from outside the engine (see interp.Value.RefID)",
					ErrUnsupportedOp, i, name)
			}
			r := args[i].toRef(in)
			got, terr := typeOfRef(r, fmt.Sprintf("%q parameter %d", name, i))
			if terr != nil {
				return nil, terr
			}
			if !matchRefType(got, castTarget(p, in.mod)) {
				return nil, fmt.Errorf("interp: %q parameter %d is %s, got %s", name, i, p, got)
			}
			locals.refs[i] = r
			continue
		}
		if args[i].Type != p {
			return nil, fmt.Errorf("interp: %q parameter %d is %s, got %s", name, i, p, args[i].Type)
		}
		if p == binary.V128 {
			// decision 0024: a v128 parameter's high half crosses through Value.Hi, never
			// through Bits alone — Bits/Hi is this boundary's own hi/lo pair, mirroring
			// frame.num/numHi.
			locals.numHi[i] = args[i].Hi
		}
		locals.num[i] = args[i].Bits
	}

	st := &stack{
		// Sized from the body rather than grown from empty, so that the common case costs
		// exactly one allocation per call. Not a correctness property — append would be
		// correct — but 0002 chose this form on a measurement, and paying an amortized
		// regrow per call in the hot loop would spend what the measurement bought.
		//
		// **This used to claim the bound was *sufficient*, on the ground that "an
		// instruction pushes at most one numeric slot". Decision 0024 made that false and
		// nothing updated the sentence** — a v128 push occupies two slots, so a body dense
		// in v128 producers can exceed `len(fn.Body)` and regrow. Found sweeping for grave
		// #242's shape, which is the same false premise (one value, one slot) in prose
		// rather than in arithmetic; recorded rather than silently corrected because the
		// two are the same defect and one of them cost 23 vectors.
		//
		// Left at `len(fn.Body)` deliberately: it is still a good *estimate*, the regrow is
		// amortized and correct, and re-tightening it (to a scan for v128 producers, or a
		// flat factor) is a claim about allocation counts that belongs to `make bench` and
		// benchstat, not to a grave's sweep. Stated, not fixed, and not asserted as a
		// number nobody ran.
		num: make([]uint64, 0, len(fn.Body)),

		// Propagation site 3 of 3 (decision 0050). A boundary `Invoke` runs on the host's thread,
		// and all three of this engine's stack creation sites do, because there is no second thread
		// to run on: T-1's spawn is withheld (see `thread`'s doc comment). Its entry would be the
		// fourth site and the first not to use `&in.host`, which is why
		// `TestEveryStackCreationSiteCarriesAThread` derives its domain rather than listing these three.
		t: &in.host,
	}
	numResults, refResults := countByArray(ft.Results)
	// §3 SP-2's denominator, decision 0067 and the repair for #592: `Stop` asks whether *every* caller
	// on this thread is suspended, and this pair is what tells it how many there are.
	//
	// **Here and not at the top of the function, which is the load-bearing part of the placement.** The
	// delegation above returns `ext.owner.invokeIndex(...)`, so exactly one `in.run` executes per
	// re-export chain while every instance in the chain has its own `thread`. Counting on entry would
	// leave a delegating instance's thread with a caller counted and no guest code running on it — an
	// unsatisfiable `blocked == callers` for a thread that will never poll and never arrive, so every
	// `Stop` on that instance would wait out its whole deadline. Wrapping `in.run` counts exactly the
	// thread the guest code runs on.
	//
	// `defer` rather than a plain call after `run`, because every error return between here and the end
	// of the function is a caller that has stopped executing.
	st.t.enterCall()
	defer st.t.leaveCall()
	if err := in.run(fn, locals, st, numResults, refResults); err != nil {
		return nil, err
	}
	if len(st.num) != numResults {
		// #9's arity check, arriving late. Stated as the layering debt it is rather than
		// dressed as `type mismatch`.
		return nil, fmt.Errorf("%w: %q declares %d numeric results and left %d values on the stack",
			ErrNotValidated, name, numResults, len(st.num))
	}
	if len(st.refs) != refResults {
		return nil, fmt.Errorf("%w: %q declares %d reference results and left %d references on the stack",
			ErrNotValidated, name, refResults, len(st.refs))
	}
	out := make([]Value, len(ft.Results))
	for i := len(out) - 1; i >= 0; i-- {
		t := ft.Results[i]
		if t.IsRef() {
			out[i] = fromRef(st.popRef(), t)
			continue
		}
		if t == binary.V128 {
			hi, lo := st.popV128()
			out[i] = Value{Type: t, Bits: lo, Hi: hi}
			continue
		}
		out[i] = Value{Type: t, Bits: st.popNum()}
	}
	return out, nil
}

// exportedFunc resolves an export name to a function index.
func (in *Instance) exportedFunc(name string) (uint32, bool) {
	return in.exportedIndex(binary.ExternFunc, name)
}

// exportedGlobal resolves an export name to a global index, for Global (#323).
func (in *Instance) exportedGlobal(name string) (uint32, bool) {
	return in.exportedIndex(binary.ExternGlobal, name)
}

// exportedIndex resolves an export name to an index within one extern kind's index space.
//
// Linear over the export section, and deliberately not indexed: a map built per instance would
// be paid by every module and read by the handful the harness invokes. If a Go guest's export
// table ever becomes the hot path, the index belongs in Instance and is built once — which is
// the shape this comment exists to record rather than the thing to build now.
//
// **Parameterized on the kind rather than written twice**, which is graves #78/#105/#106's rule
// (`globalFor`, `memoryFor`) applied one layer out: `exportedGlobal` arrived as a copy of
// `exportedFunc` differing in one constant, and two functions that know how to turn a name into
// an index is exactly the pair those graves are about. The `e.Kind` test is not incidental —
// a module may export a function and a global under the *same* name, so dropping it would make
// the caller's kind depend on declaration order.
func (in *Instance) exportedIndex(kind binary.ExternKind, name string) (uint32, bool) {
	for _, e := range in.mod.Exports {
		if e.Kind == kind && e.Name == name {
			return e.Index, true
		}
	}
	return 0, false
}

// funcType resolves a function's declared type.
//
// The two failures here are #9's — a type index past the end of the type section, and a type
// index naming a struct or an array rather than a functype — and both are reported as the
// layering debt rather than as spec verdicts. The second is reachable today in the all-gates-on
// lane specifically, because `Module.Types` keeps struct and array slots so that GC type indices
// do not shift; a function declaring one of those slots is a module the validator rejects and
// the decoder accepts.
func (in *Instance) funcType(fn *binary.Func) (*binary.FuncType, error) {
	if int(fn.TypeIndex) >= len(in.mod.Types) {
		return nil, fmt.Errorf("%w: function declares type %d of %d",
			ErrNotValidated, fn.TypeIndex, len(in.mod.Types))
	}
	ct := &in.mod.Types[fn.TypeIndex]
	if ct.Kind != binary.CompFunc {
		return nil, fmt.Errorf("%w: function declares type %d, which is a %s",
			ErrNotValidated, fn.TypeIndex, ct.Kind)
	}
	return &ct.Func, nil
}
