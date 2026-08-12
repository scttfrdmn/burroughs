// Package interp executes the internal form (decision 0002, #7).
//
// # What this package is, and the one thing it must not become
//
// The decoder's `[]binary.Instr` **is** the program. There is no second lowering pass and no
// bytecode: 0002 Q1 chose internal-form rewrite, and `binary.DecodeModule` already produces it —
// immediates pre-decoded, branch targets resolved to indices in the same slice. So the interpreter's
// input is the artifact with the conformance record (4162 vectors) rather than a translation of it.
//
// That is also the boundary this package must not cross. The text front end retains a *shallow*
// instruction list for emission (`internal/text/code.go`), and it is deliberately not this form; the
// two must never be joined. A module reaches here by having been **decoded**, which is 0011's
// sole-authority rule and the reason the text path encodes to bytes rather than handing over a
// structure.
//
// # Why the value stack is two arrays, from the first line
//
// 0002 pins this as a *consequence*, not a footnote, and at Scott's direction rather than as a
// preference: **references MUST live in a parallel array from the first line of interpreter code.**
// A Go pointer stored in a `uint64` is invisible to the garbage collector, and pure Go (no cgo)
// leaves no escape hatch — a collector that cannot see a reference is free to collect the object it
// names, so a single-array stack is a use-after-free waiting for WasmGC.
//
// The cost of getting this wrong is not a bug but a rewrite: by the time §8's M-4 arrives, every
// opcode touching the stack has assumed one array. So the split exists here before a single
// reference type is executable, which is the *whole* point — it is cheap now and GC-precision
// surgery later.
//
// **The array is live as of `global.get`, and this paragraph is kept in the past tense rather than
// deleted.** It read: *"v0 executes no reference-typed instruction, so `refs` is allocated and never
// pushed to … a reader finding it unused should read this paragraph rather than delete it (#6's
// declared-and-tracked ruling; #7 tracks the growth)."* That was true for the whole interval it was
// written for, and the record of a deferral that was honoured is worth more than a tidy file — the
// pin was made before any consumer existed and it held, which is the claim the paragraph was making.
//
// What retired it is `global.get` of an externref (`global.wast:30`, `get-r`): the first slot this
// engine pushes onto `refs` and pops back off. Note the shape, because it is the second time this
// exact deferral was spent by an unpredicted consumer — `ref` the *type* was first constructed by
// element-segment initialization, not by a reference opcode, and `refs` the *array* is first pushed
// by a global, not by `ref.null`. A pin's retirement condition is a guess about which consumer
// arrives first; the pin itself is not.
package interp

import (
	"fmt"
	"math"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// ref is a reference value's slot in the parallel array.
//
// A struct rather than a bare `any` or a pointer, because a reference is *two* facts — whether it is
// null, and what it points at — and `nil` cannot carry the first without conflating `ref.null func`
// with an absent slot. The stack still pushes none of these; the type exists so that the array it
// lives in has an element type chosen by the design rather than by the first opcode that needs one.
//
// **The declared-and-tracked suppression is spent, and it is worth recording that it was spent by a
// consumer it did not predict.** The nolint read "retired by the first reference opcode (#7)", and no
// reference opcode has an arm yet — what constructs a `ref` is `table.go`'s element-segment
// initialization, which fills a new table with nulls and evaluates `ref.func`/`ref.null` out of a
// segment's expressions without any of them ever reaching the stack. So the *type* found its
// consumer one layer below the one named. That is the intended ending either way (#6's ruling, and
// the `reader.u64` precedent at binary.go:678): a deferral retired by a production caller, never by
// a suppression outliving its reason.
//
// That last sentence read *"`stack.refs` is still unpushed, which is #7's half"* — true when written
// and now spent too, by `global.get` of an externref. Both halves of the pin therefore retired to
// production callers and neither retired to the consumer its comment predicted, which is the
// paragraph in the package doc above.
type ref struct {
	// Null is whether this is a null reference. Separate from Addr being zero, because address
	// zero is a legitimate function index and `ref.null func` is a *value*, not an absence.
	Null bool

	// Addr is what the reference names, read according to the reference's static type — a
	// function index for `funcref`, and for the GC types an object handle this package does not
	// have yet.
	Addr uint32

	// Inst is the instance Addr's index space belongs to (0017 Q2, grave #163). A funcref is a
	// *pair* — which instance, which index — because index 3 names two different functions once
	// one instance's table can hold a slot another instance's element segment filled. Nil for a
	// null reference, since there is nothing to name; every non-null construction site sets it
	// to the instance whose index space Addr was read from, which is always in scope at
	// construction — `segmentRefs`'s index form is an `*Instance` method, and `ref.func`'s arm
	// in exec.go has the running instance in hand. Those are now the *only* two sites, #241
	// having deleted the third (`constExprRef`, which built the same pair by pattern-matching an
	// element expression); an expression-form element segment reaches the arm instead.
	//
	// This is option A of the three the ADR priced: `ref{Null, Addr}` gains an `*Instance`
	// field, matching `instance.ml:21`'s `funcinst = moduleinst … Func.t` — the reference's
	// funcref carries its module instance too. Rejected: a flat store with Addr indexing into it
	// (denser, but makes Addr mean something the module cannot resolve on its own — the
	// premature-generality option 0016 also declined) and copying the callee's Func into the
	// slot (wrong once the callee reads its own memory or globals, not merely slower).
	//
	// **The GC-precision pin this field is *for*.** 0002 requires references to live in a
	// parallel array so the collector can trace them (package doc above); Inst is the first
	// thing that array has ever held that the collector genuinely must trace, an *Instance
	// outliving the frame that pushed it. A funcref surviving in a table after its originating
	// instance's own call frames have returned is the ordinary case, not an edge one.
	Inst *Instance

	// Exc is the exception this reference names, non-nil exactly when the reference's
	// runtime type is exnref and Null is false (0022 §1). Addr/Inst are meaningless for
	// this case, the same way Inst's own comment states about Addr needing an instance to
	// resolve against — an exception's identity is the allocation itself, resolving
	// against nothing but its own tag and payload.
	//
	// A new field rather than a union with Inst, matching 0020's own rejected-alternative
	// reasoning one payload kind later: `ref` grows one field per new payload kind it must
	// carry, not a second representation, so a funcref never sets Exc and an exnref never
	// sets Addr/Inst.
	Exc *excObj

	// Obj is the struct or array instance this reference names, non-nil exactly when the
	// reference's runtime type is a concrete aggregate and Null is false — decision 0020, and
	// `gcobj.go` carries the model's own reasoning.
	//
	// The fourth payload field, and its arrival is the `Exc` comment above being right about the
	// shape rather than about one instance of it: `ref` grows one field per payload kind it must
	// carry, so a funcref never sets Obj and a struct never sets Addr/Inst/Exc. That the
	// prediction held one rung later is the reason it is worth keeping written down.
	//
	// **This is the field the collector most obviously must trace**, and therefore the strongest
	// vindication of 0002's parallel-array pin: a `*gcObj` is guest-allocated, guest-reachable
	// only through this array, and outlives every frame that touched it once it is stored in a
	// table or a global. Under a tagged-uint64 representation it would be a pointer the Go
	// collector cannot see, i.e. a use-after-free with no diagnostic. Here it is an ordinary Go
	// pointer field in an ordinary Go struct.
	Obj *gcObj
}

// stack is the value stack: 0002 Q3's bare `uint64` slots plus the pinned parallel reference array.
//
// **Two arrays, one logical stack, and the validator decides which array a slot uses.** That is
// 0002's phrasing and it matters here: `num` and `refs` are not two stacks that happen to be pushed
// in step, they are one stack whose slots live in whichever array their static type requires. Since
// the validator knows every slot's type statically (#9), no slot is ever ambiguous and no tag is
// stored — which is what makes the numeric side a bare `uint64` rather than a tagged union.
//
// Depth is therefore tracked **per array**, not once. A single `sp` would be wrong in both
// directions: it would over-count the numeric array when a reference is pushed, and it cannot be
// used to index either array. The invariant a validated module guarantees is that each pop finds its
// value in the array its type names — not that the two depths bear any relation to each other.
type stack struct {
	// num holds every numeric slot — i32, i64, f32, f64, and v128's two halves when SIMD
	// arrives. Bare `uint64` rather than a tagged value: the reinterpretation is the opcode's
	// business, which is what `math.Float64bits` and friends are for below.
	num []uint64

	// refs holds every reference slot.
	//
	// **No longer empty, and the suppression is gone rather than kept.** It carried
	// `//nolint:unused // pinned by 0002 before its first consumer; retired by the first
	// reference opcode (#7)` — a claim about a design the code could not yet demonstrate — and
	// `global.get` of an externref is that first consumer, so the directive is deleted rather
	// than left to suppress nothing. That is the honest end for a suppression, and the same
	// end `pc`'s `intrange` directive came to one file over: the prose keeps the reason, the
	// directive does not outlive its subject.
	refs []ref

	// numSeq/refSeq/nextSeq/tracking are grave #206's fix (decision 0023): a monotonic push
	// sequence number per slot, lazily activated. `drop` (exec.go, opcode 0x1a) has no static
	// signal for which array holds the logical top-of-stack — no validator (#9) exists to
	// supply one, and the wire form carries no immediate at all — so it consults these instead.
	//
	// **Lazy, not always-on, and the reason is measured rather than assumed** (0023's own
	// `dropbench`): tagging every push costs +72-75% on this array's own operations regardless
	// of whether a reference is ever pushed, because 0 of the numeric core's corpus needs one at
	// all (exec.go's own header) — so the tax would be paid by nearly every function for a
	// question nearly no function asks. `tracking` stays false, `numSeq` stays nil, until the
	// first `pushRef` — mirroring `frame`'s own lazy `refs`/`isRef` allocation (value.go) for
	// the identical reason.
	//
	// **Retired, not merely made cheaper, the day #9 exists** — 0023's own stated consequence:
	// a validated `drop` reads a statically-known operand type and needs none of this.
	numSeq   []uint64
	refSeq   []uint64
	nextSeq  uint64
	tracking bool
}

// frame is a call's local-variable storage: 0002's parallel-array split applied to *locals*
// rather than to the value stack, since #196/#197.
//
// **Two arrays, one logical vector, same reasoning `stack` already gives.** A local's declared
// type decides which array its index lives in — `global.go`'s `get`/`set` make the identical
// choice for a global's storage, and `IsRef()` is the predicate in both places, per 0002's
// GC-traceability pin (package doc above): a Go pointer inside a `uint64` slot is invisible to
// the collector, and a ref-typed local surviving across many instructions of straight-line code
// is the ordinary case, not an edge one.
//
// **Both arrays are sized `total` and indexed by the same flat local index**, unlike `stack`'s
// two independently-sized arrays — a local's index is fixed for the function's lifetime (it
// names a *slot*, not a stack position), so `num[i]` and `refs[i]` are simply the two possible
// homes for local `i`'s value and exactly one is ever read or written, per isRef. This wastes one
// slot's width in the array a local's index does *not* use, which is the cost 0002's own
// `ref`-versus-`uint64` slot-width tradeoff already accepts elsewhere for simplicity over
// density (see ref's own doc comment on the rejected "flat store with Addr indexing into it").
//
// **`refs` and `isRef` are allocated lazily, not always alongside `num`.** Every numeric-only
// function — which is the overwhelming majority of the corpus, per exec.go's own header (0 of
// the 139-opcode numeric core's answerable population needs a reference) — pays nothing beyond
// two `nil` slices' zero cost; only a function whose signature or locals contain a reference
// type allocates either array at all. See newFrame.
type frame struct {
	num  []uint64
	refs []ref

	// isRef is a per-index bitmap: isRef[i] is true when local i is a reference type. Built
	// once at frame construction (newFrame) from the callee's params and declared locals,
	// rather than re-derived at every local.get/set/tee — the same "ask once, not per access"
	// reasoning Func.EachLocal's own doc comment gives for iterating rather than indexing a
	// flattened local-type vector. Nil when the frame has no reference locals at all, which
	// getLocal/setLocal/teeLocal treat identically to "every entry false".
	isRef []bool

	// numHi and isV128 are decision 0024's answer for a v128 local: `frame.num` is flatly
	// indexed by local index (`num[i]` is local `i`'s fixed slot for the function's whole
	// lifetime), so the stack's own "two adjacent slots" trick does not transfer — there is no
	// "push a second slot" operation for a flat array. Instead `numHi[i]` carries a v128 local's
	// high 64 bits (Bits/`num[i]` carries the low), gated by `isV128`'s bitmap exactly as `refs`
	// is gated by `isRef` — nil until the first v128 local, and every non-v128 local's access
	// cost is unchanged. See getLocal/setLocal/teeLocal's own v128 arms.
	numHi  []uint64
	isV128 []bool
}

// newFrame allocates a call frame sized for total locals, allocating the reference array and
// the isRef bitmap only when the function actually declares a reference-typed parameter or
// local — see frame's own doc comment for why that laziness exists. paramTypes is the callee's
// declared parameter types (for the isRef bitmap's leading entries) and eachLocal iterates the
// declared locals beyond them, mirroring exactly the two sources invokeIndex's and invoke's own
// pre-#196/#197 code read to build `locals` — this is one place doing what both used to do
// separately, per the one-authority reasoning memoryFor's own doc comment gives.
func newFrame(total uint64, paramTypes []binary.ValType, eachLocal func(func(idx uint32, vt binary.ValType) bool)) *frame {
	f := &frame{num: make([]uint64, total)}
	var refTotal, v128Total uint64
	for _, p := range paramTypes {
		if p.IsRef() {
			refTotal++
		} else if p == binary.V128 {
			v128Total++
		}
	}
	eachLocal(func(_ uint32, vt binary.ValType) bool {
		if vt.IsRef() {
			refTotal++
		} else if vt == binary.V128 {
			v128Total++
		}
		return true
	})
	if refTotal > 0 {
		f.refs = make([]ref, total)
		f.isRef = make([]bool, total)
		for i, p := range paramTypes {
			f.isRef[i] = p.IsRef()
		}
		eachLocal(func(idx uint32, vt binary.ValType) bool {
			f.isRef[uint64(len(paramTypes))+uint64(idx)] = vt.IsRef()
			return true
		})
		// **Grave #246: a reference local defaults to `ref.null`, and Go's zero `ref` is not
		// null.** `make([]ref, total)` yields `{Null: false, Addr: 0, Inst: nil}`, which this
		// package reads as *a funcref to function 0 of the current instance* — see `ref`'s own
		// doc comment, which makes the zero value non-null deliberately, because a null and a
		// reference to function 0 must not be the same bits. So the allocation above is a claim
		// about function 0 unless this loop follows it. `eval.ml` builds locals from
		// `default_value t`, and `default_value` of a nullable reference type is `Some NullRef`
		// (`value.ml:150-152`); a non-nullable reference local is non-defaultable and validation
		// rejects the function, so every reference local that can exist defaults to null.
		//
		// **The type was right and the allocation site did not honour it** — `table.go:134`
		// already writes `ref{Null: true}` into every fresh table slot for this exact reason, so
		// the sibling had the arm and this site re-derived the allocation without it (graves
		// #105/#243). One rung up, the lesson generalizes past references: *where a domain's
		// default differs from Go's zero value, every allocation site must say so, because the
		// type cannot.*
		//
		// Params are overwritten by the caller's arguments a moment later, so filling them is
		// redundant rather than wrong; the loop covers the whole array rather than only the
		// declared locals because "which indices are params" is a second fact to get right and
		// the redundant writes cost nothing a call does not already pay.
		for i := range f.isRef {
			if f.isRef[i] {
				f.refs[i] = ref{Null: true}
			}
		}
	}
	if v128Total > 0 {
		f.numHi = make([]uint64, total)
		f.isV128 = make([]bool, total)
		for i, p := range paramTypes {
			f.isV128[i] = p == binary.V128
		}
		eachLocal(func(idx uint32, vt binary.ValType) bool {
			f.isV128[uint64(len(paramTypes))+uint64(idx)] = vt == binary.V128
			return true
		})
	}
	return f
}

// getLocal, setLocal, and teeLocal read/write local index idx, dispatching on the frame's own
// isRef/isV128 bitmaps exactly as global.go's get/set dispatch on a global's declared type — the
// local.get/set/tee arms in exec.go are these three's only callers, and none of them needs to
// know which array (or array pair, for v128) backs an index because the frame does.
func (f *frame) getLocal(idx uint64, st *stack) {
	if len(f.isRef) > 0 && f.isRef[idx] {
		st.pushRef(f.refs[idx])
		return
	}
	if len(f.isV128) > 0 && f.isV128[idx] {
		st.pushV128(f.numHi[idx], f.num[idx])
		return
	}
	st.pushNum(f.num[idx])
}

func (f *frame) setLocal(idx uint64, st *stack) error {
	if len(f.isRef) > 0 && f.isRef[idx] {
		if err := st.needRef(1); err != nil {
			return err
		}
		f.refs[idx] = st.popRef()
		return nil
	}
	if len(f.isV128) > 0 && f.isV128[idx] {
		if err := st.needNum(2); err != nil {
			return err
		}
		f.numHi[idx], f.num[idx] = st.popV128()
		return nil
	}
	if err := st.needNum(1); err != nil {
		return err
	}
	f.num[idx] = st.popNum()
	return nil
}

// teeLocal is setLocal without consuming the stack's top — local.tee's own semantics, peeking
// rather than popping so the value survives for the next instruction.
func (f *frame) teeLocal(idx uint64, st *stack) error {
	if len(f.isRef) > 0 && f.isRef[idx] {
		if err := st.needRef(1); err != nil {
			return err
		}
		f.refs[idx] = st.refs[len(st.refs)-1]
		return nil
	}
	if len(f.isV128) > 0 && f.isV128[idx] {
		if err := st.needNum(2); err != nil {
			return err
		}
		// Peek the top two slots without popping: pushV128's own order is hi then lo, so lo is
		// the stack's true top and hi sits one slot below it.
		f.numHi[idx] = st.num[len(st.num)-2]
		f.num[idx] = st.num[len(st.num)-1]
		return nil
	}
	if err := st.needNum(1); err != nil {
		return err
	}
	f.num[idx] = st.num[len(st.num)-1]
	return nil
}

// len reports the frame's declared local count, for badLocal's bounds message.
func (f *frame) len() int { return len(f.num) }

// pushNum pushes a raw numeric slot.
//
// Every numeric push funnels through here rather than appending at each opcode, so the one place
// that knows the stack's representation is the stack. The i32 case is worth naming: a 32-bit value
// occupies a full slot with its high bits **zero**, because `i32.const -1` is `0xFFFFFFFF` and not
// `0xFFFFFFFFFFFFFFFF` — an i32 is not a sign-extended i64, and conflating them makes `i64.extend_i32_u`
// a no-op when it is not one. The decoder already sign-extends s32 immediates into `Imm0`, so the
// truncation belongs at the push rather than in each opcode.
//
// **Tags the slot with a push sequence number, but only when `tracking` is on** (0023, grave
// #206) — `drop`'s own signal for which array holds the logical top, and lazily maintained for
// 0023's own measured reason: tagging unconditionally costs the same whether or not a reference
// is ever pushed, since the cost is the extra append/reslice on this array's own operations, not
// anything about references.
func (s *stack) pushNum(v uint64) {
	s.num = append(s.num, v)
	if s.tracking {
		s.numSeq = append(s.numSeq, s.nextSeq)
		s.nextSeq++
	}
}

// popNum pops a raw numeric slot.
//
// **No underflow check, and that is a layering decision rather than an omission.** Stack underflow
// is `type mismatch` — a *validation* verdict (#9) — so a validated module cannot underflow, and
// checking here would either duplicate the validator or make the interpreter reject a module for a
// reason the spec assigns to another layer. Until #9 exists this is a real precondition on the
// caller, which is why `Invoke` refuses to run a module it has not been able to check the arity of;
// stated here because an unchecked pop is the kind of thing that reads as an oversight.
func (s *stack) popNum() uint64 {
	v := s.num[len(s.num)-1]
	s.num = s.num[:len(s.num)-1]
	if s.tracking {
		s.numSeq = s.numSeq[:len(s.numSeq)-1]
	}
	return v
}

// pushV128 pushes a v128 value as two adjacent numeric slots, hi then lo, sharing **one**
// sequence number across both slots (decision 0024).
//
// **Never two independent `pushNum` calls.** That would give the two slots two different push
// sequence numbers under 0023's tracking, and a `drop` or `branch` landing between them would see
// two different ages for what is logically one value — grave #206's shape one layer up, since
// nothing would signal that the two slots are a single unit rather than two independent numerics.
// This function is the one place a v128 enters the stack, so the atomicity is structural rather
// than a discipline every future 0xfd arm has to remember. TestV128SharesOneSequenceNumberAcrossBothSlots
// (0024's own vecbench) pins this by mutation: reverting to two independent appends is watched to
// fail before this shape is trusted.
//
// Hi first, lo second, matching `Instr.Imm0`/`Imm1`'s own little-endian half ordering
// (`binary/instr.go`'s `immV128` decode arm) and `popV128`'s own pop order below.
func (s *stack) pushV128(hi, lo uint64) {
	// **A v128 push activates tracking itself, exactly as pushRef does, and for the identical
	// reason: `drop` needs the two slots' shared sequence number to recognize a v128 top even
	// in a function that never pushes a reference at all.** Without this, a v128-only function
	// would leave `tracking` false, `numSeq` nil, and `drop`'s own v128-pair check (which reads
	// `numSeq`) could never fire — reproducing grave #206's shape for the population 0023's own
	// gating was never measured against, since no v128 value existed when that ADR was written.
	//
	// **Grave #215: the interaction-defect class.** 0023's activation condition named its
	// historical trigger (the first reference pushed) rather than the property it protects
	// (every live multi-slot unit carries orderable sequence numbers) — refs were the first
	// customer, not the only possible one, and 0024 introduced a second without either ADR's
	// author knowing to check the other's gate. The fix states the property, not the trigger:
	// this activation runs on *any* multi-slot push, matching the guarantee's real scope.
	if !s.tracking {
		s.tracking = true
		s.numSeq = make([]uint64, len(s.num))
		for i := range s.numSeq {
			s.numSeq[i] = s.nextSeq
			s.nextSeq++
		}
	}
	seq := s.nextSeq
	s.nextSeq++
	s.num = append(s.num, hi, lo)
	s.numSeq = append(s.numSeq, seq, seq)
}

// popV128 pops a v128 value pushed by pushV128, returning its hi and lo halves in that order.
//
// Two `popNum` calls in LIFO order — lo pops first (it was pushed last), hi second — which is
// why the return is `(hi, lo)` rather than pop order: the caller wants the value's own two
// halves, not the mechanics of how they left the stack.
func (s *stack) popV128() (hi, lo uint64) {
	lo = s.popNum()
	hi = s.popNum()
	return hi, lo
}

// pushRef pushes a reference slot, and popRef pops one.
//
// The reference half of `pushNum`/`popNum`, deliberately written to the same contract: the pop
// does **not** check depth, because underflow is `type mismatch` and that verdict is #9's (see
// popNum, which states the reasoning this pair inherits rather than restating it). Callers that
// cannot rely on a validator call needRef first, exactly as the numeric arms call needNum.
//
// These are the first writers to `refs` since 0002 pinned it — the event that field's comment
// named as its retirement condition for the `unused` suppression.
//
// **The first `pushRef` in a frame's life activates sequence tracking and backfills `numSeq`**
// (0023) — every numeric slot already on the stack needs a sequence number too, ascending in
// push order starting below `nextSeq`, or the invariant "every live slot has one" breaks the
// moment `drop` is asked about a stack mixing pre- and post-activation slots. Correct because
// nothing has been popped between actual push order and now, so ascending backfill order matches
// the slots' real relative age.
func (s *stack) pushRef(r ref) {
	if !s.tracking {
		s.tracking = true
		s.numSeq = make([]uint64, len(s.num))
		for i := range s.numSeq {
			s.numSeq[i] = s.nextSeq
			s.nextSeq++
		}
	}
	s.refs = append(s.refs, r)
	s.refSeq = append(s.refSeq, s.nextSeq)
	s.nextSeq++
}

func (s *stack) popRef() ref {
	r := s.refs[len(s.refs)-1]
	s.refs = s.refs[:len(s.refs)-1]
	s.refSeq = s.refSeq[:len(s.refSeq)-1]
	return r
}

// drop implements opcode 0x1a: pop whichever of `num`/`refs` holds the logical top-of-stack
// value, decided by comparing the two arrays' top sequence numbers (0023, grave #206) — an
// absent array (nothing tracked yet, or empty) reads as older than everything, so a `drop` on a
// stack that has never pushed a reference is exactly today's `popNum`, at the cost of one
// length check.
//
// **Before this, `drop` always called `popNum`, silently corrupting the stack whenever the
// logical top was actually a reference** — confirmed with a three-instruction reproducer
// carrying no exception-handling machinery at all: `(ref.null func) (drop) (i32.const 7))`
// returned garbage instead of 7. See grave #206 and decision 0023 for the diagnosis and the
// measurement behind this fix.
func (s *stack) drop() {
	numTop, refTop := int64(-1), int64(-1)
	if s.tracking && len(s.numSeq) > 0 {
		numTop = int64(s.numSeq[len(s.numSeq)-1])
	}
	if len(s.refSeq) > 0 {
		refTop = int64(s.refSeq[len(s.refSeq)-1])
	}
	if refTop > numTop {
		s.popRef()
		return
	}
	// **A v128's two slots share one sequence number (decision 0024's own `pushV128`), so a
	// single matching pair at the numeric top is the signal that the logical top is one v128
	// value, not two independent numerics** — `popNum` alone would remove only the low half,
	// leaving the high half behind as a stray slot the next instruction reads as garbage. This
	// is `drop`'s own gap in 0024's ADR: the ADR named the push/pop *atomicity* but not that
	// `drop` itself, having no static operand type (#9's absence, same reason the sequence
	// numbers exist at all), must recognize the two-slot pair before popping either half.
	if s.tracking && len(s.numSeq) >= 2 && s.numSeq[len(s.numSeq)-1] == s.numSeq[len(s.numSeq)-2] {
		s.popV128()
		return
	}
	s.popNum()
}

// needRef is needNum for the reference array.
//
// **A separate check, not a shared one taking a length**, because the two arrays are one logical
// stack with *independent* depths (see the stack type): a single helper reading one array would
// answer the other's question wrongly, and the invariant a validated module gives is per-array.
// The message names references so that a shortfall is not read as a missing numeric operand.
func (s *stack) needRef(n int) error {
	if len(s.refs) < n {
		return fmt.Errorf("%w: stack has %d references, an instruction wanted %d",
			ErrNotValidated, len(s.refs), n)
	}
	return nil
}

// pushI32 pushes an i32, zero-extended into its slot.
//
// The zero-extension is the whole function. `uint64(uint32(v))` rather than `uint64(v)`: the latter
// sign-extends a negative i32 across the high 32 bits, which makes the slot hold a *different value*
// than the module's, invisibly, on every negative constant. Wrapped as a named helper so that the
// conversion is written once rather than at each of the arithmetic opcodes.
func (s *stack) pushI32(v int32) { s.pushNum(uint64(uint32(v))) }

// popI32 pops an i32 from its slot, discarding the high bits.
//
// Symmetric with pushI32, and the truncation is deliberate rather than defensive: a validated module
// only ever finds an i32 here, but `i32.wrap_i64` and the load instructions produce slots whose high
// bits are meaningful to a *different* type, so reading the low 32 is the type's own semantics.
func (s *stack) popI32() int32 { return int32(uint32(s.popNum())) }

// pushI64 and popI64 are the identity cases, present so that every opcode reads the same way.
//
// A no-op wrapper earns its place here by making the *absence* of a conversion explicit: an i64 slot
// needs no adjustment, and a reader comparing the i32 and i64 arms should see that difference stated
// rather than infer it from one arm calling a helper and the other not.
func (s *stack) pushI64(v int64) { s.pushNum(uint64(v)) }

func (s *stack) popI64() int64 { return int64(s.popNum()) }

// pushF32 pushes an f32 by its bit pattern, zero-extended.
//
// **`math.Float32bits`, never a numeric conversion**, and this is the reason the whole family exists
// as helpers. `uint64(f)` truncates a float to an integer; `Float32bits` reinterprets its bits, which
// is what a wasm value stack holds. The two compile without complaint and differ on every non-integral
// value — and every NaN, which is the case the suite tests hardest: a payload-preserving NaN is
// exactly what a numeric conversion destroys.
func (s *stack) pushF32(v float32) { s.pushNum(uint64(math.Float32bits(v))) }

func (s *stack) popF32() float32 { return math.Float32frombits(uint32(s.popNum())) }

// pushF64 and popF64 are the 64-bit pair, and the bit-pattern discipline is identical.
//
// This is the first pair the engine actually uses: `float_literals.wast:233` is
// `(assert_return (invoke "4294967249") (f64.const 4294967249))`, whose body is one `f64.const` and
// an `end`. So the first instruction Burroughs ever executes reads its immediate through
// `Float64frombits`, and getting that reinterpretation wrong would produce a plausible number
// (4294967249 is integral) while being wrong for every value that is not.
func (s *stack) pushF64(v float64) { s.pushNum(math.Float64bits(v)) }

func (s *stack) popF64() float64 { return math.Float64frombits(s.popNum()) }

// Value is one wasm value crossing the host boundary, carrying its type.
//
// **Tagged, unlike the stack**, and the asymmetry is the point rather than an inconsistency. On the
// stack the validator knows every slot's type statically, so a tag would be redundant weight in the
// hottest structure in the engine. At the boundary there is no validator: a host calling `Invoke`
// hands over values whose types must be *checked* against the function's signature, and a caller
// receiving results has no other way to know what it got. So the tag exists exactly where the static
// knowledge stops.
//
// This is the shape the §4 host contract will need too, which is why it is exported now rather than
// kept internal.
//
// **Widened for a reference value, since #196/#197** — the internal `ref` struct's fields, made
// safe to export by *narrowing* rather than by exposing `*Instance` directly (§4's host contract
// cannot construct a `ref{Inst: *Instance}` from outside this package, and should not need to):
// Null and RefID are exported directly, and what `ref.Inst`/`ref.Addr` mean externally is scoped
// down to exactly what #197's own population measurement found the corpus needing — see RefID's
// own doc comment for the reasoning, and invokeIndex/toRef for the two directions this crosses.
type Value struct {
	Type binary.ValType

	// Bits is the value's representation, read according to Type — the same discipline as the
	// numeric stack, so crossing the boundary is a tag being attached rather than a
	// representation being converted. Unread when Type.IsRef(); a reference's representation
	// is Null/RefID below, never a bit pattern, because a Go pointer (what `ref.Inst` carries
	// internally) cannot honestly be flattened into a `uint64` without reintroducing the
	// GC-invisibility hazard 0002's whole parallel-array pin exists to avoid.
	Bits uint64

	// Hi is a v128 value's high 64 bits (decision 0024) — meaningful only when Type == V128, in
	// which case Bits holds the low 64 bits. A v128 is the one type this boundary carries in two
	// words rather than one, mirroring `Instr.Imm0`/`Imm1`'s own wire-format packing and
	// `stack.pushV128`/`popV128`'s own hi/lo pair; every other numeric type leaves this zero and
	// unread, exactly as Null/RefID are unread for a numeric Value.
	Hi uint64

	// Null is this reference's nullity — meaningful only when Type.IsRef(). A null reference
	// crosses the boundary as `Value{Type: <ref type>, Null: true}` in both directions and
	// needs nothing else, which is also the *only* funcref shape #197's own population
	// measurement found any corpus vector passing as an **argument**: 0 vectors pass a
	// non-null funcref through `Invoke`, so a non-null funcref argument is out of this
	// widening's scope exactly as `readRefConst`'s own doc comment states for `ref.func` as a
	// harness-side literal — the two scope statements are the same measurement, cited once.
	Null bool

	// RefID is an externref's opaque host identity — this Value's mirror of `ref.extern N`'s
	// N, present so a caller can construct and inspect a non-null externref without reaching
	// into the package-internal `ref`/`Extern` types. Meaningful only when Type == ExternRef
	// and !Null. Zero is a legitimate identity, exactly as spec.Val.Extern's own comment
	// states, so RefID must never be read as "unset" — Null is the field that means that.
	//
	// **No funcref-identity field, and that is a scope boundary stated rather than hidden.** A
	// non-null funcref crossing this boundary would need to name *which* function in *which*
	// instance (ref.Addr, ref.Inst) — the pair 0017 Q2/grave #163 established a funcref
	// always needs — and the only two ways to spell that publicly are exposing `*Instance`
	// (declined; see this type's own doc comment) or accepting a function *index in the
	// callee's own instance* (which is what a hypothetical `ref.func $name`-as-argument would
	// operationally mean). The corpus needs neither: measured over testdata/spec, 0 vectors
	// pass a non-null funcref as an invoke argument. Building either shape now would be
	// premature generality for a consumer that does not exist (0006), so this stays a stated
	// gap rather than a silent one — a future non-null funcref argument surfaces as
	// ErrUnsupportedOp at invokeIndex's parameter loop, named, rather than as a wrong value.
	RefID uint32
}

// F64 constructs an f64 Value from a float.
func F64(v float64) Value { return Value{Type: binary.F64, Bits: math.Float64bits(v)} }

// F32 constructs an f32 Value from a float.
func F32(v float32) Value { return Value{Type: binary.F32, Bits: uint64(math.Float32bits(v))} }

// I32 constructs an i32 Value, zero-extended into its slot.
func I32(v int32) Value { return Value{Type: binary.I32, Bits: uint64(uint32(v))} }

// I64 constructs an i64 Value.
func I64(v int64) Value { return Value{Type: binary.I64, Bits: uint64(v)} }

// NullRef constructs a null reference Value of the given reference type — the only funcref
// argument shape the corpus needs (see Value.RefID's doc comment) and one of two externref
// argument shapes.
func NullRef(t binary.ValType) Value { return Value{Type: t, Null: true} }

// ExternRef constructs a non-null externref Value carrying the given opaque host identity.
func ExternRef(id uint32) Value { return Value{Type: binary.ExternRef, RefID: id} }

// Float64 reads a Value as an f64, by bit pattern.
func (v Value) Float64() float64 { return math.Float64frombits(v.Bits) }

// Float32 reads a Value as an f32, by bit pattern.
func (v Value) Float32() float32 { return math.Float32frombits(uint32(v.Bits)) }

// Int32 reads a Value as an i32.
func (v Value) Int32() int32 { return int32(uint32(v.Bits)) }

// Int64 reads a Value as an i64.
func (v Value) Int64() int64 { return int64(v.Bits) }

// toRef converts a reference-typed Value to the internal ref shape, resolving a non-null
// funcref's instance to in — the caller's own instance, per RefID's doc comment on why a
// cross-instance funcref argument is out of scope: an externally-supplied funcref can only
// mean "a function index in the callee's own instance", and the corpus never supplies a
// non-null one at all, so this path is reachable only via a future widening's own arm, not by
// anything in today's corpus.
func (v Value) toRef(in *Instance) ref {
	if v.Null {
		return ref{Null: true}
	}
	if v.Type == binary.ExternRef {
		// externref's "address" is the opaque host identity, stored in the same Addr field a
		// funcref's function index uses — the two never collide because a slot's Kind decides
		// which reading applies, exactly as ref's own Addr field comment already states for
		// the function-index case. Inst is left nil: an externref never resolves through an
		// instance's index space, so nothing here would ever read it.
		return ref{Addr: v.RefID}
	}
	return ref{Addr: uint32(v.Bits), Inst: in}
}

// fromRef converts an internal ref back to its public Value, at static type t.
func fromRef(r ref, t binary.ValType) Value {
	if r.Null {
		return Value{Type: t, Null: true}
	}
	if t == binary.ExternRef {
		return Value{Type: t, RefID: r.Addr}
	}
	return Value{Type: t, Bits: uint64(r.Addr)}
}

// Equal reports whether two values are identical, **bit for bit**, which is what
// `assert_return` means.
//
// **Not `==` on the float, and this is the one comparison in the engine where that would be wrong
// twice over.** `assert_return`'s expected value is a bit pattern: `NaN == NaN` is false in Go so a
// float comparison fails a vector that should pass, and `+0.0 == -0.0` is true so it passes a vector
// that should fail. Both directions appear in the suite. Comparing `Bits` gets both right and needs
// no special cases — which is the argument for holding values as bit patterns in the first place.
//
// The type is compared too, because `i32 0` and `f32 0.0` share a bit pattern and are different
// values; a comparison that ignored the tag would score a wrongly-typed result green.
//
// **Reference-typed values compare Null and RefID instead of Bits (#196/#197)** — Bits is unread
// for a reference Value (see its own doc comment), so a bit-for-bit comparison here would compare
// two zero values and call every pair of distinct non-null funcrefs equal.
func (v Value) Equal(w Value) bool {
	if v.Type != w.Type {
		return false
	}
	if v.Type.IsRef() {
		return v.Null == w.Null && v.RefID == w.RefID
	}
	return v.Bits == w.Bits
}
