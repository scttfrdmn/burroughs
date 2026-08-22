package text

import (
	"math"
	mathbits "math/bits"
	"strconv"
	"strings"
)

// The immediate readers: the reference's numeric literal conversions (parser.mly:37-43),
// which are the layer #63's largest buckets live in.
//
// **Why these are hand-written against `ixx.ml` rather than delegated to `strconv`.**
// `strconv.ParseUint(s, 0, bits)` looks like exactly this function and is wrong for wat in
// two directions, both measured before a line of this file was written, and both invisible
// to every reject-direction vector the suite has:
//
//   - **Base 0 reads a leading zero as octal.** `strconv.ParseUint("0755", 0, 64)` is 493;
//     the reference's `parse_int` (ixx.ml:322) is hex-if-`0x`, else **decimal**, so it is
//     755. wat has no octal. The suite *names* this — `int_literals.wast:10` exports a
//     function called `i32.not_octal` asserting `(i32.const 010)` is 10 — but it names it
//     with `assert_return`, which phase v0 cannot execute, so the vector that would catch
//     this cannot run yet. A wrong value here would decode, validate, and compute the wrong
//     answer silently.
//   - **Base 0 rejects `08` and `09` outright**, which are perfectly legal wat nats. That is
//     the accept-direction defect that matters more (a decoder that rejects valid modules is
//     worse than one that misses an invalid one), and *no* `assert_malformed` vector can see
//     it, because these are valid.
//
// Base 10 fixes the first and cannot read `0x…` at all. There is no `strconv` mode that is
// the reference's grammar, so the grammar is written out.
//
// Underscores are not handled here, and that is a *premise*, not an omission: the lexer's
// `matchNum` is `digit ('_'? digit)*` (match.go:89), so every underscore reaching this
// function sits between two digits, and `1__0`, `_1`, `1_`, `0x1_` never lex. The reference
// admits them at this layer (`of_string` skips `_` anywhere) and relies on its lexer for the
// same reason. TestUnderscorePlacementIsTheLexersJob pins the premise rather than trusting
// it — a claim about another layer that a refactor there could quietly end.

// parseNat converts a `nat` lexeme to a uint64, reporting whether it fits in `bits`.
//
// The reference does this with `of_string_u` at three widths (`nat8`, `nat32`, `nat64`,
// parser.mly:37-43) whose only difference is the width and the error message, so this is
// width-parameterized for the reason decision 0003's LEB readers are: *when two fields
// disagree about a value, the suite has handed you a bidirectional control*. Identical bytes
// are `i8 constant out of range` as a lane index and legal as a memory offset.
//
// Overflow is detected before it happens rather than by checking the result, because the
// reference does: `require (le_u num (shr_u minus_one (of_int 4)))` before each hex shift
// (ixx.ml:312) and a divrem-based bound before each decimal multiply (:319). Checking after
// the fact would already have wrapped.
func parseNat(s string, bits uint) (uint64, bool) {
	limit := ^uint64(0)
	if bits < 64 {
		limit = 1<<bits - 1
	}
	if s == "" {
		return 0, false
	}
	// parse_int (ixx.ml:322): "0x" selects hex, everything else is decimal. No octal.
	//
	// `len(s) >= 2`, not `> 2`, because the reference's guard is `i + 2 <= len` — so bare
	// `"0x"` selects the hex branch and parses as **0**, an empty digit loop returning its
	// accumulator. This reader's first draft wrote `> 2`, which sent `"0x"` down the decimal
	// path to be rejected at the `x`. Unreachable either way (`nat = num | "0x" hexnum`,
	// lexer.mll:95, needs at least one hexdigit), so no vector can tell the two apart — which
	// is exactly why it is written the reference's way rather than the way that happens to be
	// untestable. An accept-direction divergence no oracle can see is the one to fix by
	// reading, since nothing else will ever report it.
	if len(s) >= 2 && s[0] == '0' && s[1] == 'x' {
		var n uint64
		for i := 2; i < len(s); i++ {
			c := s[i]
			if c == '_' {
				continue
			}
			d, ok := hexDigit(c)
			if !ok {
				return 0, false
			}
			if n > limit>>4 {
				return 0, false
			}
			n = n<<4 | uint64(d)
		}
		return n, true
	}
	var n uint64
	for i := range len(s) {
		c := s[i]
		if c == '_' {
			continue
		}
		if c < '0' || c > '9' {
			return 0, false
		}
		d := uint64(c - '0')
		if n > (limit-d)/10 {
			return 0, false
		}
		n = n*10 + d
	}
	return n, true
}

func hexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return 0xa + c - 'a', true
	case c >= 'A' && c <= 'F':
		return 0xa + c - 'A', true
	}
	return 0, false
}

// fitsAsIntConst reports whether an integer `num` literal is in range for an `iN.const`.
//
// **A face on intConstBits, not a second reading of the range.** The predicate came first and the
// conversion arrived with the code section (#8); merging them rather than writing the range twice is
// the one-concept-one-trigger rule at the point where it matters most — a range check that admits a
// literal the converter mis-encodes is an accept-direction defect, and two copies of ixx.ml's
// composed bounds is exactly how the two would come to disagree. The range's derivation is
// intConstBits'.
func fitsAsIntConst(s string, bits uint) bool {
	_, ok := intConstBits(s, bits)
	return ok
}

// intConstBits converts an integer `num` literal to the bit pattern an `iN.const` immediate holds,
// reporting whether it is in range.
//
// The range is **asymmetric and wider than either signed or unsigned alone**: `[-2^(bits-1),
// 2^bits - 1]`. That is not a convenience, it is `of_string`'s two checks composed
// (ixx.ml:328-342), and both halves are needed:
//
//   - A `-` literal parses its magnitude unsigned and then requires `ge_s (sub n one) minus_one`
//     (:334) — which admits `n = 2^(bits-1)` exactly (its predecessor is signed-max) and
//     rejects `2^(bits-1)+1` (whose predecessor is signed-min). So the negative bound is the
//     magnitude, not the value.
//   - An unsigned literal is bounded by `parse_dec`/`parse_hex` overflowing `Rep`, then by
//     `require (upper = zero || upper = mask || upper = sign)` (:341). For a Rep exactly
//     `bits` wide that admits the whole unsigned range, so `i32.const 4294967295` is legal.
//
// Hence one function rather than a signed and an unsigned variant: a literal is in range if it
// satisfies *either* bound, and asking only one of them would reject a whole half of the legal
// spellings. `i32.const -2147483648` and `i32.const 4294967295` are both fine and neither fits
// the other's bound.
//
// **The return is a bit pattern, not a value, and the two ranges above are why.** `i32.const
// 4294967295` and `i32.const -1` denote the same i32 — the reference's `of_string` produces one
// `Rep`, a bit pattern, from both — so a signed return type would have to reject one of the two
// legal spellings or widen to hold a value no i32 has. The caller sign-interprets at the width it
// knows: `w.s32(int32(uint32(v)))` for 32 bits, `w.s64(int64(v))` for 64, which is `s32 c` and
// `s64 c` at encode.ml:576-579.
//
// Negation is two's complement on the pattern (`-n` on a uint64, wrapping by definition), which is
// the same operation `Rep.neg` is and is *not* the sign-bit XOR floats use — the difference between
// the two negations is a fact about the representations, and getting it backwards would give
// `i32.const -1` the pattern 0x80000001.
func intConstBits(s string, bits uint) (uint64, bool) {
	neg := false
	switch {
	case strings.HasPrefix(s, "-"):
		neg, s = true, s[1:]
	case strings.HasPrefix(s, "+"):
		// `of_string` admits a leading `+` (:331) where `of_string_u` forbids it (:349). This is
		// the `of_string` path — `num` calls `I32.of_string` (lexer.mll:310), not the `_u` form —
		// so `i32.const +1` is legal and a nat immediate like an index is not.
		s = s[1:]
	}
	n, ok := parseNat(s, 64)
	if !ok {
		return 0, false
	}
	if neg {
		// Magnitude ≤ 2^(bits-1). Written as a comparison against a shift rather than as a
		// negated value so 64-bit works without wrapping: at bits=64 the bound is 2^63, which
		// has no positive int64.
		if n > uint64(1)<<(bits-1) {
			return 0, false
		}
		return -n, true
	}
	if bits >= 64 {
		return n, true // parseNat already bounded it at 64 bits
	}
	// `n < 2^bits` rather than `n <= 2^bits - 1`, which is the same set and does not depend on
	// the guard above to avoid wrapping: at bits=64 the shift would overflow in the `- 1` form's
	// intermediate. Taking gocritic's simplification because it removes an action-at-a-distance,
	// not merely because it is shorter.
	if n >= uint64(1)<<bits {
		return 0, false
	}
	return n, true
}

// fitsAsFloatConst reports whether a `num` literal is in range for an `fN.const`.
//
// `of_string` (fxx.ml:325-332) strips a leading sign and hands the rest to
// `of_signless_string` (:305), whose arms are, in order: `inf`, `nan`, `nan:0x…`, and
// everything else. The verdict is `is_inf` on the *target-width* value (:323) — so the rule is
// "reject what overflows", and there is **no underflow check at all**: `f32.const 1e-46`
// rounds to zero and is legal. A reader that rejected underflow would be rejecting valid
// modules, and no `assert_malformed` vector could say so.
//
// The sign is dropped rather than re-applied because the reference's negation happens *after*
// the range check and cannot change it: `is_inf` is symmetric, and `const.wast:319`/`:323`
// carry `0x1p128` and `-0x1p128` as separate vectors with the same expectation.
//
// **`inf` and `nan` are accepted by arms that run before the `is_inf` check**, which is the
// only reason `f32.const inf` is well-formed. Ordering the arms any other way rejects it.
//
// **A face on floatConstBits**, for fitsAsIntConst's reason: the arms are the same arms, and a
// predicate holding its own copy of them is how a literal comes to pass the range check and be
// mis-encoded. The four arms are now written out in signlessFloatBits.
func fitsAsFloatConst(s string, bits uint) bool {
	_, ok := floatConstBits(s, bits)
	return ok
}

// floatConstBits converts a float `num` literal to the IEEE bit pattern an `fN.const` immediate
// holds, reporting whether it is in range.
//
// **This is `of_string` (fxx.ml:325-332) and it is a bit-pattern function all the way down**, which
// is the fact the predicate face did not need and the encoder cannot do without. Three of the four
// arms have no float value at all in the reference — `nan` is `pos_nan`, `nan:0x…` is
// `Rep.logor payload bare_nan`, and the sign is `neg x = Rep.logxor x Rep.min_int` (:212-213), a
// **XOR with the sign bit on the pattern**. So `-nan:0x200000` is built, not computed.
//
// # What the bit-pattern representation actually buys, measured rather than asserted
//
// This paragraph replaces a wrong one, and the wrong one is worth quoting because it was the
// plausible thing to believe: it said a float-valued implementation "would collapse every NaN
// payload to one quiet NaN", and named the sign as a place it would break. Both halves were checked
// against the mutation the claim predicts, and **neither failed** — so the claim was retired and
// re-derived. The corrections:
//
//   - **Negation is not a hazard at all.** Go's unary minus on a float32 is *exhaustively* the
//     sign-bit XOR: over all 2^32 patterns, including every NaN payload and both zeroes, 0 differ.
//     So `-nan:0x2abcde` survives an arithmetic negation intact, and the XOR here is chosen for
//     matching the reference's *derivation* rather than for fixing a defect. `-0` → 0x8000_0000
//     still reads as a bug and still is not one.
//   - **The collapse is real but it lives in the `float64` round trip, not in the negation.**
//     Narrowing is where a payload dies: `float32(math.Float64frombits(0x7ff0000000012345))` is
//     0x7fc00000, and so is every other f64 NaN — all payloads map to one quiet f32 NaN. An
//     implementation that carried literals as `float64` and narrowed at the end would therefore
//     lose exactly the nine `nan:0x…` vectors `float_literals.wast` states patterns for. Returning
//     a width-sized pattern from the arm that knows the width is what avoids it.
//
// The narrower true statement is that three arms *have no float to be valued as*, which is a fact
// about the reference's structure and is sufficient reason on its own.
//
// The finite path is `float_of_string_prevent_double_rounding` then `of_float` then the `is_inf`
// check. Go's `strconv.ParseFloat(t, bits)` is a single correctly-rounded conversion, so it produces
// what the reference's double-rounding correction exists to produce — and passing `bits` rather than
// 64-and-narrow is load-bearing: 24 of the corpus's 1295 parseable distinct `f32.const` spellings
// are double-rounding-sensitive, all of them in `const.wast` (`:444` rounds to `+0x1.000002p-50`
// where the two-step path gives `+0x1.000000p-50`).
//
// The sign is applied *after* the range check, per the arm order, so `-0x1p128` and `0x1p128` are
// rejected identically (`const.wast:319`/`:323`) — what fitsAsFloatConst's comment already recorded
// from the predicate side.
//
// Agreement is **accept-direction** — a module with the wrong constant in it is well-formed, so no
// reject vector can report the defect — and it is checked by
// TestFloatConstBitsMatchTheSuitesBitPatterns, which reads `float_literals.wast` as a table of bit
// patterns (`i32.reinterpret_f32` being the identity on bits) and `const.wast`'s 300 adjacent-line
// pairs as a table of roundings.
func floatConstBits(s string, bits uint) (uint64, bool) {
	if s == "" {
		return 0, false // `if s = "" then failwith "of_string"` (fxx.ml:326)
	}
	// `of_string`'s sign handling (fxx.ml:328-331): strip it, convert the remainder as signless,
	// then `if s.[0] = '+' then x else neg x`. The sign bit is the *width's*, so it is computed
	// here rather than passed down.
	negate := false
	if s[0] == '+' || s[0] == '-' {
		negate = s[0] == '-'
		s = s[1:]
	}
	n, ok := signlessFloatBits(s, bits)
	if !ok {
		return 0, false
	}
	if negate {
		n ^= uint64(1) << (bits - 1) // `Rep.logxor x Rep.min_int`
	}
	return n, true
}

// signlessFloatBits is `of_signless_string` (fxx.ml:305-323), whose arms are the reason the sign is
// stripped by the caller: three of them never see a float.
func signlessFloatBits(s string, bits uint) (uint64, bool) {
	// The three special forms, as bit patterns from f32.ml/f64.ml. `pos_nan` is the *quiet* NaN —
	// bare exponent plus the mantissa's top bit — which is what a bare `nan` denotes; `nan:0x…`
	// ors its payload onto `bare_nan` instead, so `nan` and `nan:0x400000` coincide at f32 and
	// `nan:0x1` does not. Written as literals per width because that is how the reference has them.
	var posInf, posNaN, bareNaN uint64
	switch bits {
	case 32:
		posInf, posNaN, bareNaN = 0x7f80_0000, 0x7fc0_0000, 0x7f80_0000
	case 64:
		posInf, posNaN, bareNaN = 0x7ff0_0000_0000_0000, 0x7ff8_0000_0000_0000, 0x7ff0_0000_0000_0000
	default:
		// No third float width exists in the tracked set; fitsAsNaNPayload's default carries the
		// argument for why this is a refusal rather than a fallthrough.
		return 0, false
	}
	if s == "inf" {
		return posInf, true
	}
	if s == "nan" {
		return posNaN, true
	}
	// `String.length s > 6 && String.sub s 0 6 = "nan:0x"` (fxx.ml:310) — the reference's guard, for
	// the reason fitsAsFloatConst's comment gives: bare `"nan:0x"` falls *through* to
	// `float_of_string` and fails there.
	if len(s) > 6 && s[:6] == "nan:0x" {
		if !fitsAsNaNPayload(s[4:], bits) {
			return 0, false
		}
		// `Rep.logor x bare_nan` (:311). The payload is re-parsed rather than threaded out of the
		// predicate: fitsAsNaNPayload's three arms are the range check, and parseNat is
		// side-effect-free, so asking twice cannot disagree with itself the way two copies of the
		// range would.
		n, _ := parseNat(s[4:], bits)
		return n | bareNaN, true
	}
	f, ok := finiteFloat(s, bits)
	if !ok {
		return 0, false
	}
	// `of_float` then `if is_inf x then failwith` (fxx.ml:322-323), and the bits come from the
	// *target width's* representation — `math.Float32bits` for f32, so a value strconv already
	// rounded to float32 is stored as four bytes rather than eight.
	if bits == 32 {
		return uint64(math.Float32bits(float32(f))), true
	}
	return math.Float64bits(f), true
}

// finiteFloat is `of_signless_string`'s last arm: strip underscores, convert, reject an infinity.
//
// Split out so both the bit-pattern path above and the predicate below name one conversion. The
// underscore stripping and the `p0` graft are the two places Go's grammar differs from OCaml's, and
// both are derivations rather than choices — see fitsAsFloatConst.
func finiteFloat(s string, bits uint) (float64, bool) {
	// `String.concat "" (String.split_on_char '_' s)` (fxx.ml:321): the underscores are stripped
	// *here*, not skipped digit-by-digit as parseNat does. Same premise about placement either
	// way — TestUnderscorePlacementIsTheLexersJob covers both readers.
	//
	// **The strip is load-bearing in the reject direction, which is the reverse of the obvious
	// reading.** Removing it does not break any legal literal on this corpus: Go's ParseFloat
	// already accepts underscores in the *well-placed* positions wat allows, so of 67 distinct
	// underscore-bearing finite `fN.const` spellings in the suite, 33 parse identically either
	// way and all 34 that differ are ones Go *rejects* and the strip makes acceptable —
	// `_100`, `99_`, `1__000`, `1_.0`, `1._0`. Those are the `assert_malformed` "unknown
	// operator" vectors, and they never reach here: the lexer's `float` rule refuses the token
	// first, which is the premise the test above pins. So the strip is doing the reference's
	// derivation faithfully and, on the current corpus, would be unobservable if deleted.
	// Stated because a future reader measuring it would find it apparently dead code.
	t := strings.ReplaceAll(s, "_", "")
	// **Go's hex float syntax is narrower than wat's**: `strconv.ParseFloat` requires a binary
	// exponent, so `0x1.8` and `0xff` are syntax errors to it and perfectly legal to the
	// reference, whose `float_of_string` is OCaml's and does not. Grafting `p0` is exponent-free
	// by construction (2^0 = 1) and is the one repair that does not touch the mantissa. Measured
	// before it was written: without it, `f32.const 0x1.8` is a *valid module rejected*, the
	// direction the suite cannot report.
	if strings.HasPrefix(t, "0x") && !strings.ContainsAny(t, "pP") {
		t += "p0"
	}
	v, err := strconv.ParseFloat(t, int(bits))
	if err != nil {
		// Overflow arrives as ErrRange with an infinite v, which the check below already
		// catches; a *syntax* error means this reader and the lexer's `float` rule disagree
		// about the grammar, and the honest answer to that is still a rejection — it is what
		// `float_of_string`'s own `Failure` produces, which `num` (parser.mly:53) renders as
		// `constant out of range`. Not swallowed silently: the verdict is a reject either way,
		// so a disagreement shows up as a red vector rather than as an accepted malformed
		// module.
		return 0, false
	}
	// The reference's actual predicate (fxx.ml:323), asserted on the value rather than inferred
	// from strconv's error taxonomy — *verdict channel and mechanism channel are different
	// instruments*, and `err` is the mechanism's.
	if math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// fitsAsNaNPayload reports whether a `nan:0x…` payload is in range, per of_signless_string's
// three `raise (Failure …)` arms (fxx.ml:310-318). s begins at the `0x`.
//
// **All three arms collapse to one message, and that is the reference's own doing**, not a
// simplification here: they raise `Failure`, and `num` (parser.mly:53) renders every `Failure`
// as `constant out of range`. So the payload's three distinct diagnoses are discarded upstream
// before any vector sees them — `const.wast:419` (zero) and `:428` (exponent overlap) expect
// the same string. The arms are still written out separately, because a reader that merged them
// into one bound would be right about today's messages and wrong about the range.
//
// The payload is read with parseNat at the *Rep's* width, which is what `Rep.of_string` is for
// F32 (`Int32`) and F64 (`Int64`). Only the hex spelling can arrive: `Int32.of_string` would
// also take decimal and `0o`/`0b`, but the lexer's float rule is `"nan:" "0x" hexnum`
// (lexer.mll:106), so those are unreachable and are not modelled — a premise, named, not an
// omission.
func fitsAsNaNPayload(s string, bits uint) bool {
	n, ok := parseNat(s, bits)
	if !ok {
		return false // Rep.of_string overflowing its width
	}
	if n == 0 {
		return false // "nan payload must not be zero" (:312)
	}
	// "must not overlap with exponent bits" (:314): `Rep.logand x bare_nan <> Rep.zero`.
	// bare_nan is the exponent field with the mantissa and sign clear — f32.ml's 0x7f80_0000
	// and f64.ml's 0x7ff0_0000_0000_0000, which is `((1<<exp)-1) << mantissa` with
	// exp = bits - mantissa - 1 (fxx.ml:82).
	var bareNaN uint64
	switch bits {
	case 32:
		bareNaN = 0x7f80_0000
	case 64:
		bareNaN = 0x7ff0_0000_0000_0000
	default:
		// No third float width exists in the tracked set, and a default of "accept" here would
		// be a range check that silently stopped checking. Sibling of constWidth's zero.
		return false
	}
	if n&bareNaN != 0 {
		return false
	}
	// "must not overlap with sign bit" (:316): `x < Rep.zero`, a *signed* comparison on the
	// Rep. Reachable rather than subsumed by the arm above — 0x8000_0000 misses every exponent
	// bit and is still negative as an Int32.
	return n < uint64(1)<<(bits-1)
}

// constWidth maps an `iN.const`/`fN.const` mnemonic to its width and whether it is a float.
//
// The reference recovers this from *which lexer arm built the token* (lexer.mll:308-319), all
// four of which produce one CONST kind carrying a different closure. Here the mnemonic's text
// carries the same information, so this is a translation of the closure's identity rather than a
// second source of truth. A zero width means "not a spelling this knows", which the caller
// treats as an error instead of defaulting.
func constWidth(mnemonic string) (bits uint, isFloat bool) {
	switch mnemonic {
	case "i32.const":
		return 32, false
	case "i64.const":
		return 64, false
	case "f32.const":
		return 32, true
	case "f64.const":
		return 64, true
	}
	return 0, false
}

// vecShapeLanes maps a VECSHAPE lexeme to its lane count *and lane width*, and whether the
// lanes are floats (lexer.mll:152-157, V128.num_lanes at v128.ml:22, of_strings at :499).
//
// Three facts, not one, and the first draft of this function returned only the count — which is
// the half `wrong number of lane literals` needs and *not* the half `constant out of range`
// needs. `of_strings` dispatches on the shape to pick a per-lane converter: `I8.of_string` for
// `i8x16`, `I64.of_string` for `i64x2`, `F32.of_string` for `f32x4`. So a lane's range is the
// shape's business at two independent widths, and the count alone cannot answer it.
//
// The suite is emphatic about this and would have caught the omission: `simd_const.wast:130`
// wants `v128.const i8x16 0x100 …` rejected — sixteen literals, correct count, each one byte
// too large — and `:150` wants `i16x8 0x10000` rejected while `0x100` in an `i16x8` is legal.
// A count-only reader accepts both.
//
// **The lane width is not the mnemonic's**, which is the seam with constImm: `v128.const` lexes
// to one VEC_CONST kind whose closure takes the shape as a *parameter* (lexer.mll:321-324),
// where the four `iN.const`/`fN.const` mnemonics each carry a closure with the width already
// bound. Same information, arriving by different routes, and routing it wrong is invisible in
// the accept direction.
func vecShapeLanes(shape string) (lanes int, bits uint, isFloat, ok bool) {
	switch shape {
	case "i8x16":
		return 16, 8, false, true
	case "i16x8":
		return 8, 16, false, true
	case "i32x4":
		return 4, 32, false, true
	case "i64x2":
		return 2, 64, false, true
	case "f32x4":
		return 4, 32, true, true
	case "f64x2":
		return 2, 64, true, true
	}
	return 0, 0, false, false
}

// offsetEqValue strips the `offset=` prefix from an OFFSET_EQ_NAT lexeme.
//
// The lexer keeps the whole lexeme as written (`"offset="(nat as s)`, lexer.mll:812, hands the
// *capture* to the parser), and this package's tokens carry the full text because every error
// message quotes the input verbatim — grave #36's rule. So the split happens here, at the one
// place that wants the value.
func offsetEqValue(text string) string { return strings.TrimPrefix(text, "offset=") }

// parseAlign reads an `align=N` lexeme, applying the reference's power-of-two check.
//
// `align` (parser.mly:532) is the one immediate reader that *rejects on a semantic property*
// rather than on width: `nat64` first, then `Lib.Int64.is_power_of_two_unsigned` (lib.ml:331),
// which is `n <> 0 && n land (n - 1) = 0`. So `align=0` fails as "not a power of two" — zero is
// not one — and that single check owns both of the board's alignment buckets:
//
//	46 vectors in align.wast expect the bare string "alignment"
//	22 vectors in simd_align.wast expect "alignment must be a power of two"
//
// The suite matches by prefix (0003 as amended by ADR 0045), and the reference's message *begins with* the
// shorter one, so one check answers both. That is not luck: the shorter expectation is a prefix
// the suite chose deliberately, and decision 0003 records prefix-matching as the reason not to
// special-case it.
//
// **What this reader deliberately does not check, with the seam named.** The remaining 54
// alignment vectors expect "alignment must not be larger than natural", and that check is the
// *validator's* — `valid.ml:389`, not `parser.mly`. A parser answering it would be answering a
// question from the wrong layer, and would have to know each instruction's natural width to do
// it. Those vectors stay red until validation exists, and they are named in #63's reconciliation
// with the component they wait on rather than netted out.
//
// **It returns the exponent now, because the encoder is that consumer** (#8 tier 2). The value was
// deliberately discarded while this stratum returned errors and nothing else (decision 0011), on
// the stated ground that "a returned value nobody reads is the unreachable-error shape wearing a
// result"; the ground was sound and the condition it named has ended.
//
// **The log2 is taken here rather than by the caller, and that is where the reference takes it.**
// `align`'s action is `Some (Int64.to_int (Lib.Int64.log2_unsigned n))` — the production converts,
// so what the closure receives is already an exponent, and `encode.ml:221`'s `memop` writes
// `of_int align` straight into the flags field with no further arithmetic. Converting at this one
// site keeps the number the same number on both sides of the parse. A caller doing its own
// `mathbits.TrailingZeros` would be a second implementation of an authority's one-line action.
//
// The exponent is meaningful only when ok is true. `n=0` fails the power-of-two check, and
// `mathbits.Len64(0)-1` would be -1, so the pow2 verdict guards the value rather than the value
// implying the verdict.
func parseAlign(text string) (align int, ok, isNat bool) {
	// The lexeme is "align=" + nat; the lexer's matchEqNat guarantees the prefix and a
	// non-empty nat, so this slice is safe and its emptiness would be a lexer defect.
	const prefix = "align="
	n, isNat := parseNat(text[len(prefix):], 64)
	if !isNat {
		return 0, false, false
	}
	if n == 0 || n&(n-1) != 0 {
		return 0, false, true
	}
	// log2_unsigned of an exact power of two: the index of its one set bit.
	return mathbits.Len64(n) - 1, true, true
}
