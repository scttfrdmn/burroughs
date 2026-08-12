// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package interp

// The three i31 opcodes of the 0xfb region — rung 4 of the GC ladder (#255), and the first
// reference kind in this engine that is not a pointer to anything.
//
// Named for `opTableFB`'s reason: `fb 1d` and `fb 1e` differ by one bit and by whether the read
// sign-extends, which is the whole question a reader has.
const (
	opRefI31  = 0x1c
	opI31GetS = 0x1d
	opI31GetU = 0x1e
)

// i31Mask is the mask `i31.ml:9`'s `of_i32` applies — `Int32.to_int i land 0x7fff_ffff`.
//
// **A construction-time truncation, not a read-time one**, which is `aggr.ml`'s narrow-on-store
// contract arriving at a second site: the stored payload is *already* 31 bits, so `i31.get_u` is the
// identity and only `i31.get_s` does arithmetic. #250's M15 falsification is the reason that sentence
// is here rather than assumed — the claim "a read-time mask makes the store-time one unobservable"
// was made about `Pack.U` in this package's other narrowing pair and turned out to be false in both
// halves.
const i31Mask = 0x7fff_ffff

// trapNullI31 is `i31.get_s`/`i31.get_u` on a null reference — `eval.ml:667-668`'s
// `Trapping "null i31 reference"`.
//
// **A third null-trap string, and the reference is the reason it is not one of the other two.**
// `ref.as_non_null` says "null reference" (trapNullRef), `call_ref` says "null function reference"
// (trapNullFuncRef), and `I31Get` says "null i31 reference" — three sites, three texts, each
// oracle-covered by the file that asks for it. `i31.wast:53-54` is
// `(assert_trap (invoke "get_u-null") "null i31 reference")`, so this is one of the cases #38's
// refinement names: the expected string *is* the whole message, and collapsing the three would fail
// vectors in three files and pass nothing.
var trapNullI31 = &Trap{Reason: "null i31 reference"}

// execRefI31 is `ref.i31` — `eval.ml:663-664`'s `Ref (I31.I31Ref (I31.of_i32 i))`.
//
// The 31-bit truncation happens **here**, at construction, so nothing downstream has to remember it:
// `i31.get_u` is then a plain widening and `ref.eq` compares two already-narrowed payloads. The
// alternative — store all 32 bits and mask at every read — would make `ref.eq` wrong for a pair whose
// payloads differ only in bit 31, which is a wrong answer with no trap and no arity check anywhere
// near it. **Measured, not argued**: relocate the mask into `popI31` and every one of `i31.wast`'s
// fourteen value vectors still passes (54, unmoved) while `ref.eq` starts comparing 32-bit payloads —
// `i31op_test.go`'s derived row is the only witness there is.
//
// One construction site and no helper beside it, deliberately: the const-expression path does not need
// a second one, because `runConst` runs the expression through the full dispatch loop rather than
// matching it (`constexpr.go`), so a `(ref.i31 …)` global initializer reaches *this* arm. That was a
// forecast in #255 that reading killed — it had been listed as a second arm rung 4 would owe — and a
// shared `pushI31` written for it would have been a helper with one caller wearing generality's
// clothes. A free function with no receiver, because an i31 payload resolves against nothing at all:
// unlike every other reference kind here, there is no instance to look anything up in.
func execRefI31(st *stack) error {
	if short := st.needNum(1); short != nil {
		return short
	}
	st.pushRef(ref{I31: uint32(st.popI32()) & i31Mask, IsI31: true})
	return nil
}

// popI31 is the shared prologue of `i31.get_s` and `i31.get_u` — the null trap and the kind check,
// `eval.ml:666-670`'s two match arms.
//
// The *extension* is deliberately not a parameter: `eval.ml` has one `I31Get ext` arm because OCaml
// can carry `Pack.extension` in the opcode, and the faithful Go shape is two arms sharing this helper,
// each spelling its own extension where a reader can see it. A `fieldExt` parameter would import
// `extNone` into a family that has no such case, and an unreachable `default` is a control that
// asserts nothing.
func popI31(what string, st *stack) (uint32, error) {
	if short := st.needRef(1); short != nil {
		return 0, short
	}
	r := st.popRef()
	if r.Null {
		return 0, trapNullI31
	}
	// Not an i31: #9's verdict, named — `notAggregate`'s own reasoning, and it now has an i31 case
	// of its own so this reports which kind actually arrived rather than defaulting to "a function
	// reference" (grave #36: a message naming a value from the input must not name a value the
	// input never held).
	if !r.IsI31 {
		return 0, notAggregate(what, "an i31 reference", r)
	}
	return r.I31, nil
}

// execI31GetU is `i31.get_u` — `i31.ml:11`'s `Pack.U -> i'`, the identity on an already-masked
// payload, so the answer is never negative. `i31.wast:41`'s `get_u(0xaaaa_aaaa) = 0x2aaa_aaaa` is the
// vector that distinguishes it from a 32-bit read.
func execI31GetU(st *stack) error {
	v, err := popI31("i31.get_u", st)
	if err != nil {
		return err
	}
	st.pushI32(int32(v))
	return nil
}

// execI31GetS is `i31.get_s` — `i31.ml:12-14`'s `Int32.(shift_right (shift_left i' 1) 1)`: shift bit
// 30 up into the sign position, then shift back **arithmetically**.
//
// Go's `>>` on a signed operand is arithmetic, so the transcription is exact; on an *unsigned* one it
// is logical and the whole extension silently disappears, which is why the conversion to `int32`
// happens before the shift and not after. `i31.wast:47`'s `get_s(0x4000_0000) = -0x4000_0000` and
// `:49`'s `get_s(0x7fff_ffff) = -1` are the two vectors that separate the readings — an unsigned
// shift answers `0x4000_0000` and `0x7fff_ffff` respectively, passing every non-negative row.
func execI31GetS(st *stack) error {
	v, err := popI31("i31.get_s", st)
	if err != nil {
		return err
	}
	st.pushI32(int32(v<<1) >> 1)
	return nil
}
