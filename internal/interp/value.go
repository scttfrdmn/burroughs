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
// v0 executes no reference-typed instruction, so `refs` is allocated and never pushed to. That is
// declared rather than left to be discovered: an empty parallel array is exactly what "pinned before
// its first consumer" looks like, and a reader finding it unused should read this paragraph rather
// than delete it (#6's declared-and-tracked ruling; #7 tracks the growth).
package interp

import (
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
// a suppression outliving its reason. `stack.refs` is still unpushed, which is #7's half.
type ref struct {
	// Null is whether this is a null reference. Separate from Addr being zero, because address
	// zero is a legitimate function index and `ref.null func` is a *value*, not an absence.
	Null bool

	// Addr is what the reference names, read according to the reference's static type — a
	// function index for `funcref`, and for the GC types an object handle this package does not
	// have yet.
	Addr uint32
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

	// refs holds every reference slot. Empty throughout v0 — see the package comment for why it
	// exists anyway, which is the reason not to delete it. Suppressed for `ref`'s reason and
	// retired by the same event.
	//
	//nolint:unused // pinned by 0002 before its first consumer; retired by the first reference opcode (#7)
	refs []ref
}

// pushNum pushes a raw numeric slot.
//
// Every numeric push funnels through here rather than appending at each opcode, so the one place
// that knows the stack's representation is the stack. The i32 case is worth naming: a 32-bit value
// occupies a full slot with its high bits **zero**, because `i32.const -1` is `0xFFFFFFFF` and not
// `0xFFFFFFFFFFFFFFFF` — an i32 is not a sign-extended i64, and conflating them makes `i64.extend_i32_u`
// a no-op when it is not one. The decoder already sign-extends s32 immediates into `Imm0`, so the
// truncation belongs at the push rather than in each opcode.
func (s *stack) pushNum(v uint64) { s.num = append(s.num, v) }

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
	return v
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
// kept internal — but it is deliberately minimal: no reference values, because v0 executes none, and
// adding them here before an opcode produces one would design the boundary from a consumer that does
// not exist (0006's load-bearing-spot rule).
type Value struct {
	Type binary.ValType

	// Bits is the value's representation, read according to Type — the same discipline as the
	// numeric stack, so crossing the boundary is a tag being attached rather than a
	// representation being converted.
	Bits uint64
}

// F64 constructs an f64 Value from a float.
func F64(v float64) Value { return Value{Type: binary.F64, Bits: math.Float64bits(v)} }

// F32 constructs an f32 Value from a float.
func F32(v float32) Value { return Value{Type: binary.F32, Bits: uint64(math.Float32bits(v))} }

// I32 constructs an i32 Value, zero-extended into its slot.
func I32(v int32) Value { return Value{Type: binary.I32, Bits: uint64(uint32(v))} }

// I64 constructs an i64 Value.
func I64(v int64) Value { return Value{Type: binary.I64, Bits: uint64(v)} }

// Float64 reads a Value as an f64, by bit pattern.
func (v Value) Float64() float64 { return math.Float64frombits(v.Bits) }

// Float32 reads a Value as an f32, by bit pattern.
func (v Value) Float32() float32 { return math.Float32frombits(uint32(v.Bits)) }

// Int32 reads a Value as an i32.
func (v Value) Int32() int32 { return int32(uint32(v.Bits)) }

// Int64 reads a Value as an i64.
func (v Value) Int64() int64 { return int64(v.Bits) }

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
func (v Value) Equal(w Value) bool { return v.Type == w.Type && v.Bits == w.Bits }
