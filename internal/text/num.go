package text

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
// The suite matches by substring (decision 0003), and the reference's message *contains* the
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
// The value is discarded rather than returned: this stratum returns errors and nothing else
// (decision 0011), so what the alignment *is* has no consumer until the encoder. Reporting only
// the verdict keeps that honest — a returned value nobody reads is the unreachable-error shape
// wearing a result.
func parseAlign(text string) (ok, isNat bool) {
	// The lexeme is "align=" + nat; the lexer's matchEqNat guarantees the prefix and a
	// non-empty nat, so this slice is safe and its emptiness would be a lexer defect.
	const prefix = "align="
	n, isNat := parseNat(text[len(prefix):], 64)
	if !isNat {
		return false, false
	}
	return n != 0 && n&(n-1) == 0, true
}
