package interp

import (
	"errors"
	"fmt"

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
	// to put in it: an imported memory (linking is contract §3, so v0 has no supplier) or a
	// declared one whose allocation failed for a reason that is #9's rather than a trap's.
	// See Instantiate on why a nil slot beats a shorter slice.
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

	// deferred holds the validation-shaped failures instantiation met and could not report,
	// because 0015's trap channel may not carry a verdict.
	//
	// **Retained rather than dropped, and read at the point of use** (memoryFor): a nil
	// memory slot with no reason attached would make an access report "memory 0 of 1" when
	// the truth is "this memory declared min above max", which is the engine being vague
	// about its own input. Every one of these becomes unreachable when #9 lands, which is
	// the same declared-and-tracked shape as ErrNotValidated itself.
	deferred error
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
	// One slot per memory *index*, filled positionally and **never skipped**: the imported
	// memories first — nil, since v0 has no linker — then the defined ones at the offset the
	// index space gives them. A failed allocation likewise leaves a nil slot rather than
	// shortening the slice, because appending only the successes would shift every later
	// memory's index — the same defect `Module.Types` keeps struct and array slots to avoid,
	// and one no board could see, since the affected vectors are ones the suite expects to
	// pass. That last clause was written before the import offset was measured, and the
	// measurement is what made it concrete rather than cautionary: 22 vectors, all "passing"
	// with the wrong memory's answer.
	memOff, tabOff := m.ImportedMems(), m.ImportedTables()
	globOff := m.ImportedGlobals()
	in := &Instance{
		mod:     m,
		mems:    make([]*memory, memOff+len(m.Memories)),
		tables:  make([]*table, tabOff+len(m.Tables)),
		globals: make([]*global, globOff+len(m.Globals)),
		elems:   make([]*elemInstance, len(m.Elems)),
		datas:   make([]*dataInstance, len(m.Datas)),
	}
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
				return nil, t
			}
			in.deferred = errors.Join(in.deferred, err)
			continue
		}
		in.globals[globOff+i] = g
	}
	for i := range m.Tables {
		tab, err := newTable(m.Tables[i])
		if err != nil {
			if t := asTrap(err); t != nil {
				return nil, t
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
				return nil, t
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
				return nil, t
			}
			// The slot is filled with an *empty* instance rather than left nil, per the field's
			// comment: a later `table.init` then reports out-of-bounds instead of panicking, and
			// the real reason travels on `deferred`.
			in.deferred = errors.Join(in.deferred, err)
			seg = &elemInstance{}
		}
		in.elems[i] = seg
	}
	for i := range m.Datas {
		in.datas[i] = &dataInstance{bytes: m.Datas[i].Init}
	}
	for i := range m.Elems {
		if err := in.runElem(i, &m.Elems[i]); err != nil {
			if t := asTrap(err); t != nil {
				return nil, t
			}
			in.deferred = errors.Join(in.deferred, err)
		}
	}
	for i := range m.Datas {
		if err := in.runData(i, &m.Datas[i]); err != nil {
			if t := asTrap(err); t != nil {
				return nil, t
			}
			in.deferred = errors.Join(in.deferred, err)
		}
	}
	return in, nil
}

// Deferred reports the failures instantiation met that could not travel the trap channel, or nil.
//
// **Exported because a caller can otherwise be told "nothing went wrong" when something did.**
// A trap answers "this module died coming to life"; a nil trap is *not* the same claim as "this
// module came to life completely". Between them sits the case this accessor exists for: an active
// data segment that could not be copied because its target memory is imported and v0 has no
// linker. Instantiation cannot trap for that — the reason is not a runtime event — and it cannot
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

func (t *Trap) Error() string { return "trap: " + t.Reason }

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
// in this comment that every one of its call sites becomes unreachable when #9 lands. A fuzz
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
// arm that this engine has (not ErrUnsupportedOp); what is missing is *linking*, which is
// contract §3 and v2-or-later work. Reporting either sibling would have named the wrong gap — one
// blames the module, the other blames a table — and the board's buckets are a work plan only
// while each key names the thing actually missing.
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
	fn, ok := in.mod.DefinedFunc(idx)
	if !ok {
		// An exported *import*. The index is in the imported range, so there is no body
		// here to run — linking is contract §3 and not this phase. Reported as an
		// engine gap rather than a module fault, because the module is fine.
		return nil, fmt.Errorf("%w: exported function %q is an import (index %d)",
			ErrUnsupportedOp, name, idx)
	}
	ft, err := in.funcType(fn)
	if err != nil {
		return nil, err
	}
	if len(args) != len(ft.Params) {
		return nil, fmt.Errorf("interp: %q takes %d arguments, got %d", name, len(ft.Params), len(args))
	}
	// The frame's locals: parameters first, then the declared locals zeroed.
	//
	// One array, not two, and unlike the value stack that is *not* a design decision yet — a
	// ref-typed local is refused below, so no reference ever needs a slot here. When one does,
	// the parallel array joins the frame for the value stack's reason (0002's pinned
	// consequence), keyed by the same index: the validator knows each local's type, so each
	// index uses exactly one of the two arrays.
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
	locals := make([]uint64, total)
	for i, p := range ft.Params {
		if p.IsRef() {
			return nil, fmt.Errorf("%w: parameter %d of %q is %s", ErrUnsupportedOp, i, name, p)
		}
		if args[i].Type != p {
			return nil, fmt.Errorf("interp: %q parameter %d is %s, got %s", name, i, p, args[i].Type)
		}
		locals[i] = args[i].Bits
	}
	// Iterated rather than indexed, because the flat reading is what the ref check is about
	// and materializing it to get one is what #138 was. `EachLocal` yields per local without
	// allocating; the early return is the iterator's own stop signal.
	var refErr error
	fn.EachLocal(func(idx uint32, vt binary.ValType) bool {
		if vt.IsRef() {
			refErr = fmt.Errorf("%w: local %d of %q is %s", ErrUnsupportedOp, idx, name, vt)
			return false
		}
		return true
	})
	if refErr != nil {
		return nil, refErr
	}

	st := &stack{
		// Sized from the body rather than grown from empty: an instruction pushes at most
		// one numeric slot, so the body's length is a sufficient bound and there is exactly
		// one allocation per call. Not a correctness property — append would be correct —
		// but 0002 chose this form on a measurement, and paying an amortized regrow per
		// call in the hot loop would spend what the measurement bought.
		num: make([]uint64, 0, len(fn.Body)),
	}
	if err := in.run(fn, locals, st, len(ft.Results)); err != nil {
		return nil, err
	}
	if len(st.num) != len(ft.Results) {
		// #9's arity check, arriving late. Stated as the layering debt it is rather than
		// dressed as `type mismatch`.
		return nil, fmt.Errorf("%w: %q declares %d results and left %d values on the stack",
			ErrNotValidated, name, len(ft.Results), len(st.num))
	}
	out := make([]Value, len(ft.Results))
	for i := len(out) - 1; i >= 0; i-- {
		t := ft.Results[i]
		if t.IsRef() {
			return nil, fmt.Errorf("%w: result %d of %q is %s", ErrUnsupportedOp, i, name, t)
		}
		out[i] = Value{Type: t, Bits: st.popNum()}
	}
	return out, nil
}

// exportedFunc resolves an export name to a function index.
//
// Linear over the export section, and deliberately not indexed: a map built per instance would
// be paid by every module and read by the handful the harness invokes. If a Go guest's export
// table ever becomes the hot path, the index belongs in Instance and is built once — which is
// the shape this comment exists to record rather than the thing to build now.
func (in *Instance) exportedFunc(name string) (uint32, bool) {
	for _, e := range in.mod.Exports {
		if e.Kind == binary.ExternFunc && e.Name == name {
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
